package previewgroup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/divergedev/diverge/api/v1alpha1"
)

const (
	// AnnotationGroupMembers stores JSON-serialized MemberRecord entries on a PreviewGroup
	// so multi-MR services can be updated on commit pushes and detached individually on MR close/merge.
	AnnotationGroupMembers = "divergedev.com/group-members"

	// LabelComposedGroup marks a PreviewGroup managed through multi-MR / multi-service composition.
	LabelComposedGroup = "divergedev.com/composed"

	// DefaultRoutingHeaderKey is the default HTTP header key for composed PreviewGroups.
	DefaultRoutingHeaderKey = "x-diverge-env"

	defaultComposedTTL = 24 * time.Hour
)

var (
	nonSlugChars  = regexp.MustCompile(`[^a-z0-9-]+`)
	multiDashSlug = regexp.MustCompile(`-{2,}`)
)

// MemberRecord tracks which PR/MR or Environment contributed a service to a PreviewGroup.
type MemberRecord struct {
	Service         string `json:"service"`
	Namespace       string `json:"namespace,omitempty"`
	Provider        string `json:"provider,omitempty"`
	Project         string `json:"project,omitempty"`
	MR              int    `json:"mr,omitempty"`
	Branch          string `json:"branch,omitempty"`
	EnvironmentName string `json:"environmentName,omitempty"`
	UpdatedAt       string `json:"updatedAt,omitempty"`
}

// JoinOptions configures adding or updating one or more services inside a PreviewGroup.
type JoinOptions struct {
	GroupName         string
	Namespace         string // Optional fallback namespace for fake/namespaced clients
	Services          []v1alpha1.PreviewGroupServiceSpec
	Members           []MemberRecord
	Source            *v1alpha1.EnvironmentSource
	HeaderKey         string
	HeaderKeyExplicit bool
	HeaderValue       string
	ExternalURL       string
	TTL               *time.Duration
	GroupDatabase     *v1alpha1.EnvironmentDatabase // Optional shared group-level database override
	ClusterRef        *v1alpha1.ClusterTargetRef
	Owner             string
	Traceparent       string
	ReplaceServices   bool // True for `preview create --upsert` (replaces full service list)
}

// JoinResult summarizes a Join operation.
type JoinResult struct {
	PreviewGroup *v1alpha1.PreviewGroup
	Created      bool
	Members      []MemberRecord
}

// LeaveOptions configures detaching services or a PR/MR from a PreviewGroup.
type LeaveOptions struct {
	GroupName       string
	Namespace       string
	ServiceNames    []string
	Project         string
	MR              int
	EnvironmentName string
}

// LeaveResult summarizes a Leave operation.
type LeaveResult struct {
	GroupName         string
	RemovedServices   []string
	RemainingServices []string
	DeletedGroup      bool
	NotFound          bool
}

// NormalizeGroupName converts a group name or Git branch slug (e.g. "feat/checkout_v2")
// into a valid Kubernetes DNS-1123 label (<= 63 chars).
func NormalizeGroupName(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("group name cannot be empty")
	}
	if strings.Contains(trimmed, "..") || strings.ContainsAny(trimmed, "\\\x00\r\n\t") {
		return "", fmt.Errorf("invalid group name %q", raw)
	}

	slug := strings.ToLower(trimmed)
	slug = strings.NewReplacer("/", "-", "_", "-", ".", "-").Replace(slug)
	slug = nonSlugChars.ReplaceAllString(slug, "-")
	slug = multiDashSlug.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		return "", fmt.Errorf("group name %q contains no alphanumeric characters", raw)
	}

	if len(slug) > 63 {
		sum := sha256.Sum256([]byte(trimmed))
		suffix := hex.EncodeToString(sum[:4]) // 8 hex chars
		prefix := strings.TrimRight(slug[:63-1-len(suffix)], "-")
		if prefix == "" {
			prefix = "pg"
		}
		slug = prefix + "-" + suffix
	}

	if errs := validation.IsDNS1123Label(slug); len(errs) > 0 {
		return "", fmt.Errorf("invalid group name %q: %s", raw, strings.Join(errs, "; "))
	}
	return slug, nil
}

// ParseMembers reads the MemberRecord list from a PreviewGroup's annotations.
func ParseMembers(pg *v1alpha1.PreviewGroup) []MemberRecord {
	if pg == nil || pg.Annotations == nil {
		return nil
	}
	raw := strings.TrimSpace(pg.Annotations[AnnotationGroupMembers])
	if raw == "" {
		return nil
	}
	var members []MemberRecord
	if err := json.Unmarshal([]byte(raw), &members); err != nil {
		return nil
	}
	return members
}

// SetMembers writes the MemberRecord list to a PreviewGroup's annotations.
func SetMembers(pg *v1alpha1.PreviewGroup, members []MemberRecord) error {
	if pg == nil {
		return nil
	}
	if len(members) == 0 {
		if pg.Annotations != nil {
			delete(pg.Annotations, AnnotationGroupMembers)
		}
		return nil
	}
	data, err := json.Marshal(members)
	if err != nil {
		return fmt.Errorf("marshal group members: %w", err)
	}
	if pg.Annotations == nil {
		pg.Annotations = make(map[string]string)
	}
	pg.Annotations[AnnotationGroupMembers] = string(data)
	return nil
}

// IsZeroDatabase reports whether an EnvironmentDatabase has no meaningful settings configured.
func IsZeroDatabase(db v1alpha1.EnvironmentDatabase) bool {
	return strings.TrimSpace(db.Mode) == "" &&
		strings.TrimSpace(db.ConnectionRef) == "" &&
		strings.TrimSpace(db.SeedSource) == "" &&
		db.MigrationJob == nil &&
		db.Atlas == nil
}

// normalizeProvider ensures EnvironmentSource.Provider satisfies the CRD enum (gitlab|github|local).
func normalizeProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "github":
		return "github"
	case "gitlab":
		return "gitlab"
	default:
		return "local"
	}
}

// ServiceFromEnvironment extracts a PreviewGroupServiceSpec and MemberRecord from an Environment CR.
// By default it preserves the Environment's per-service Database settings when non-empty.
func ServiceFromEnvironment(env *v1alpha1.Environment) (v1alpha1.PreviewGroupServiceSpec, MemberRecord, error) {
	if env == nil {
		return v1alpha1.PreviewGroupServiceSpec{}, MemberRecord{}, fmt.Errorf("environment must not be nil")
	}

	var svcName string
	if env.Spec.ServiceConfig != nil && strings.TrimSpace(env.Spec.ServiceConfig.ServiceName) != "" {
		svcName = strings.TrimSpace(env.Spec.ServiceConfig.ServiceName)
	}
	if svcName == "" {
		for _, cs := range env.Spec.Deploy.ChangedServices {
			if trimmed := strings.TrimSpace(cs); trimmed != "" {
				svcName = trimmed
				break
			}
		}
	}
	if svcName == "" && env.Labels != nil {
		svcName = strings.TrimSpace(env.Labels["divergedev.com/service"])
	}
	if svcName == "" {
		svcName = strings.TrimSpace(env.Name)
	}
	if svcName == "" {
		return v1alpha1.PreviewGroupServiceSpec{}, MemberRecord{}, fmt.Errorf("cannot determine service name from environment")
	}

	targetNS := strings.TrimSpace(env.Namespace)
	if env.Spec.ServiceConfig != nil && strings.TrimSpace(env.Spec.ServiceConfig.Namespace) != "" {
		targetNS = strings.TrimSpace(env.Spec.ServiceConfig.Namespace)
	}
	if targetNS == "" {
		targetNS = "default"
	}

	var image string
	if env.Spec.ServiceConfig != nil {
		image = strings.TrimSpace(env.Spec.ServiceConfig.Image)
	}
	if image == "" && env.Annotations != nil {
		image = strings.TrimSpace(env.Annotations["divergedev.com/image"])
	}
	if image == "" {
		return v1alpha1.PreviewGroupServiceSpec{}, MemberRecord{}, fmt.Errorf(
			"environment %q does not have a container image configured (spec.serviceConfig.image is empty)", env.Name)
	}

	spec := v1alpha1.PreviewGroupServiceSpec{
		Name:        svcName,
		Image:       image,
		Mode:        v1alpha1.ServiceModeImage,
		Namespace:   targetNS,
		AsyncRoutes: slices.Clone(env.Spec.Routing.AsyncRoutes),
	}

	if sc := env.Spec.ServiceConfig; sc != nil {
		spec.Port = sc.Port
		spec.ParentRef = sc.ParentRef
		spec.PathPrefix = sc.PathPrefix
		spec.ImagePullPolicy = sc.ImagePullPolicy
		spec.Env = slices.Clone(sc.Env)
		if sc.Resources != nil {
			spec.Resources = sc.Resources.DeepCopy()
		}
		if sc.KEDA != nil {
			spec.KEDA = sc.KEDA.DeepCopy()
		}
		switch strings.ToLower(strings.TrimSpace(sc.Protocol)) {
		case string(v1alpha1.ServiceProtocolGRPC):
			spec.Protocol = v1alpha1.ServiceProtocolGRPC
		case string(v1alpha1.ServiceProtocolHTTP):
			spec.Protocol = v1alpha1.ServiceProtocolHTTP
		}
	}

	if !IsZeroDatabase(env.Spec.Database) {
		spec.Database = env.Spec.Database.DeepCopy()
	}
	if env.Spec.Deploy.ClusterRef != nil {
		spec.ClusterRef = env.Spec.Deploy.ClusterRef.DeepCopy()
	}

	member := MemberRecord{
		Service:         svcName,
		Namespace:       targetNS,
		Provider:        env.Spec.Source.Provider,
		Project:         env.Spec.Source.Project,
		MR:              env.Spec.Source.MR,
		Branch:          env.Spec.Source.Branch,
		EnvironmentName: env.Name,
		UpdatedAt:       time.Now().UTC().Format(time.RFC3339),
	}
	return spec, member, nil
}

// getPreviewGroup fetches a PreviewGroup handling both cluster-scoped (empty namespace)
// and optional namespaced fake-client objects.
func getPreviewGroup(ctx context.Context, c client.Client, name, fallbackNamespace string) (*v1alpha1.PreviewGroup, error) {
	var pg v1alpha1.PreviewGroup
	err := c.Get(ctx, types.NamespacedName{Name: name}, &pg)
	if err == nil {
		return &pg, nil
	}
	if apierrors.IsNotFound(err) && strings.TrimSpace(fallbackNamespace) != "" {
		if nsErr := c.Get(ctx, types.NamespacedName{Name: name, Namespace: strings.TrimSpace(fallbackNamespace)}, &pg); nsErr == nil {
			return &pg, nil
		}
	}
	return nil, err
}

// Join creates a PreviewGroup or atomically merges/updates the specified services inside an existing PreviewGroup.
// By default each joining service keeps its own per-service Database settings unless opts.GroupDatabase is non-nil.
func Join(ctx context.Context, c client.Client, opts JoinOptions) (*JoinResult, error) {
	if c == nil {
		return nil, fmt.Errorf("kubernetes client is required")
	}
	groupName, err := NormalizeGroupName(opts.GroupName)
	if err != nil {
		return nil, err
	}
	if len(opts.Services) == 0 {
		return nil, fmt.Errorf("at least one service is required to join PreviewGroup %q", groupName)
	}

	services := make([]v1alpha1.PreviewGroupServiceSpec, len(opts.Services))
	for i := range opts.Services {
		svc := *opts.Services[i].DeepCopy()
		if svc.Mode == "" {
			svc.Mode = v1alpha1.ServiceModeImage
		}
		if opts.GroupDatabase != nil {
			// Explicit shared group-level database overrides per-service DB isolation
			svc.Database = nil
		}
		services[i] = svc
	}

	nowStr := time.Now().UTC().Format(time.RFC3339)
	membersByService := make(map[string]MemberRecord, len(opts.Members))
	for _, m := range opts.Members {
		if strings.TrimSpace(m.Service) == "" {
			continue
		}
		if m.UpdatedAt == "" {
			m.UpdatedAt = nowStr
		}
		membersByService[m.Service] = m
	}
	for _, svc := range services {
		if svc.Mode == v1alpha1.ServiceModeBaseline {
			continue
		}
		if _, exists := membersByService[svc.Name]; !exists {
			m := MemberRecord{
				Service:   svc.Name,
				Namespace: svc.Namespace,
				UpdatedAt: nowStr,
			}
			if opts.Source != nil {
				m.Provider = opts.Source.Provider
				m.Project = opts.Source.Project
				m.MR = opts.Source.MR
				m.Branch = opts.Source.Branch
			}
			membersByService[svc.Name] = m
		}
	}

	var result *JoinResult
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, getErr := getPreviewGroup(ctx, c, groupName, opts.Namespace)
		if getErr != nil && !apierrors.IsNotFound(getErr) {
			return getErr
		}

		if apierrors.IsNotFound(getErr) || existing == nil {
			source := v1alpha1.EnvironmentSource{
				Provider: "local",
				Branch:   groupName,
			}
			if opts.Source != nil {
				source = *opts.Source
				source.Provider = normalizeProvider(source.Provider)
				if strings.TrimSpace(source.Branch) == "" {
					source.Branch = groupName
				}
			}

			headerKey := strings.TrimSpace(opts.HeaderKey)
			if headerKey == "" {
				headerKey = DefaultRoutingHeaderKey
			}
			headerValue := strings.TrimSpace(opts.HeaderValue)
			if headerValue == "" {
				headerValue = groupName
			}

			ttlDur := defaultComposedTTL
			if opts.TTL != nil && *opts.TTL > 0 {
				ttlDur = *opts.TTL
			}

			pg := &v1alpha1.PreviewGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: groupName,
					Labels: map[string]string{
						LabelComposedGroup: "true",
					},
				},
				Spec: v1alpha1.PreviewGroupSpec{
					Source: source,
					Routing: v1alpha1.PreviewGroupRouting{
						Mode:        "header",
						HeaderKey:   headerKey,
						HeaderValue: headerValue,
						ExternalURL: strings.TrimSpace(opts.ExternalURL),
					},
					Services: services,
					Lifecycle: &v1alpha1.PreviewGroupLifecycle{
						TTL:            &metav1.Duration{Duration: ttlDur},
						CleanupOnMerge: true,
					},
					Owner: strings.TrimSpace(opts.Owner),
				},
			}
			if opts.GroupDatabase != nil {
				pg.Spec.Database = opts.GroupDatabase.DeepCopy()
			}
			if opts.ClusterRef != nil {
				pg.Spec.ClusterRef = opts.ClusterRef.DeepCopy()
			}
			if strings.TrimSpace(opts.Traceparent) != "" {
				pg.Annotations = map[string]string{
					"divergedev.com/traceparent": strings.TrimSpace(opts.Traceparent),
				}
			}

			newMembers := orderedMembers(nil, membersByService, pg.Spec.Services)
			if err := SetMembers(pg, newMembers); err != nil {
				return err
			}

			if vErrs := v1alpha1.ValidatePreviewGroupUpdate(nil, pg); len(vErrs) > 0 {
				return vErrs.ToAggregate()
			}

			if createErr := c.Create(ctx, pg); createErr != nil {
				if apierrors.IsAlreadyExists(createErr) {
					return apierrors.NewConflict(v1alpha1.GroupVersion.WithResource("previewgroups").GroupResource(), groupName, createErr)
				}
				return createErr
			}

			result = &JoinResult{
				PreviewGroup: pg,
				Created:      true,
				Members:      newMembers,
			}
			return nil
		}

		updated := existing.DeepCopy()
		if updated.Labels == nil {
			updated.Labels = make(map[string]string)
		}
		if !opts.ReplaceServices {
			updated.Labels[LabelComposedGroup] = "true"
		}

		if opts.HeaderKeyExplicit && strings.TrimSpace(opts.HeaderKey) != "" {
			updated.Spec.Routing.HeaderKey = strings.TrimSpace(opts.HeaderKey)
		} else if strings.TrimSpace(updated.Spec.Routing.HeaderKey) == "" {
			hk := strings.TrimSpace(opts.HeaderKey)
			if hk == "" {
				hk = DefaultRoutingHeaderKey
			}
			updated.Spec.Routing.HeaderKey = hk
		}
		if strings.TrimSpace(opts.HeaderValue) != "" {
			updated.Spec.Routing.HeaderValue = strings.TrimSpace(opts.HeaderValue)
		} else if strings.TrimSpace(updated.Spec.Routing.HeaderValue) == "" {
			updated.Spec.Routing.HeaderValue = groupName
		}
		if strings.TrimSpace(opts.ExternalURL) != "" {
			updated.Spec.Routing.ExternalURL = strings.TrimSpace(opts.ExternalURL)
		}
		if opts.TTL != nil && *opts.TTL > 0 {
			if updated.Spec.Lifecycle == nil {
				updated.Spec.Lifecycle = &v1alpha1.PreviewGroupLifecycle{}
			}
			updated.Spec.Lifecycle.TTL = &metav1.Duration{Duration: *opts.TTL}
		}
		if opts.GroupDatabase != nil {
			updated.Spec.Database = opts.GroupDatabase.DeepCopy()
		}
		if opts.ClusterRef != nil {
			updated.Spec.ClusterRef = opts.ClusterRef.DeepCopy()
		}
		if strings.TrimSpace(opts.Traceparent) != "" {
			if updated.Annotations == nil {
				updated.Annotations = make(map[string]string)
			}
			updated.Annotations["divergedev.com/traceparent"] = strings.TrimSpace(opts.Traceparent)
		}

		if opts.ReplaceServices {
			updated.Spec.Services = slices.Clone(services)
			if opts.Source != nil {
				src := *opts.Source
				src.Provider = normalizeProvider(src.Provider)
				if strings.TrimSpace(src.Branch) != "" {
					updated.Spec.Source = src
				}
			}
		} else {
			merged := slices.Clone(updated.Spec.Services)
			for _, incoming := range services {
				idx := slices.IndexFunc(merged, func(cur v1alpha1.PreviewGroupServiceSpec) bool {
					return cur.Name == incoming.Name
				})
				if idx >= 0 {
					// Preserve pinned spoke ClusterRef when caller did not specify one so updates don't trip ClusterRefImmutability.
					if incoming.ClusterRef == nil && merged[idx].ClusterRef != nil {
						incoming.ClusterRef = merged[idx].ClusterRef.DeepCopy()
					}
					merged[idx] = incoming
				} else {
					merged = append(merged, incoming)
				}
			}
			updated.Spec.Services = merged
		}

		priorMembers := ParseMembers(existing)
		if opts.ReplaceServices {
			priorMembers = nil
		}
		newMembers := orderedMembers(priorMembers, membersByService, updated.Spec.Services)
		if err := SetMembers(updated, newMembers); err != nil {
			return err
		}

		if vErrs := v1alpha1.ValidatePreviewGroupUpdate(existing, updated); len(vErrs) > 0 {
			return vErrs.ToAggregate()
		}

		if updErr := c.Update(ctx, updated); updErr != nil {
			return updErr
		}

		result = &JoinResult{
			PreviewGroup: updated,
			Created:      false,
			Members:      newMembers,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func orderedMembers(existing []MemberRecord, updates map[string]MemberRecord, activeServices []v1alpha1.PreviewGroupServiceSpec) []MemberRecord {
	byService := make(map[string]MemberRecord, len(existing)+len(updates))
	for _, m := range existing {
		if strings.TrimSpace(m.Service) != "" {
			byService[m.Service] = m
		}
	}
	for svcName, m := range updates {
		byService[svcName] = m
	}

	out := make([]MemberRecord, 0, len(byService))
	for _, svc := range activeServices {
		if svc.Mode == v1alpha1.ServiceModeBaseline {
			continue
		}
		if m, ok := byService[svc.Name]; ok {
			out = append(out, m)
		}
	}
	return out
}

// Leave detaches matching services from a PreviewGroup.
// When zero non-baseline services remain in the group, the PreviewGroup itself is automatically deleted.
func Leave(ctx context.Context, c client.Client, opts LeaveOptions) (*LeaveResult, error) {
	if c == nil {
		return nil, fmt.Errorf("kubernetes client is required")
	}
	groupName, err := NormalizeGroupName(opts.GroupName)
	if err != nil {
		return nil, err
	}

	var result *LeaveResult
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		pg, getErr := getPreviewGroup(ctx, c, groupName, opts.Namespace)
		if getErr != nil {
			if apierrors.IsNotFound(getErr) {
				result = &LeaveResult{GroupName: groupName, NotFound: true}
				return nil
			}
			return getErr
		}

		members := ParseMembers(pg)
		toRemove := resolveServicesToRemove(pg, members, opts)
		if len(toRemove) == 0 {
			remaining := make([]string, 0, len(pg.Spec.Services))
			for _, s := range pg.Spec.Services {
				if s.Mode != v1alpha1.ServiceModeBaseline {
					remaining = append(remaining, s.Name)
				}
			}
			result = &LeaveResult{
				GroupName:         pg.Name,
				RemainingServices: remaining,
			}
			return nil
		}

		var keptServices []v1alpha1.PreviewGroupServiceSpec
		var removedNames []string
		var remainingActive []string
		for _, svc := range pg.Spec.Services {
			if toRemove[svc.Name] {
				removedNames = append(removedNames, svc.Name)
				continue
			}
			keptServices = append(keptServices, svc)
			if svc.Mode != v1alpha1.ServiceModeBaseline {
				remainingActive = append(remainingActive, svc.Name)
			}
		}

		// Auto-delete the PreviewGroup when the last active (non-baseline) service leaves.
		if len(remainingActive) == 0 {
			if delErr := client.IgnoreNotFound(c.Delete(ctx, pg)); delErr != nil {
				return delErr
			}
			result = &LeaveResult{
				GroupName:       pg.Name,
				RemovedServices: removedNames,
				DeletedGroup:    true,
			}
			return nil
		}

		updated := pg.DeepCopy()
		updated.Spec.Services = keptServices
		var keptMembers []MemberRecord
		for _, m := range members {
			if !toRemove[m.Service] {
				keptMembers = append(keptMembers, m)
			}
		}
		if err := SetMembers(updated, keptMembers); err != nil {
			return err
		}

		if updErr := c.Update(ctx, updated); updErr != nil {
			return updErr
		}

		result = &LeaveResult{
			GroupName:         pg.Name,
			RemovedServices:   removedNames,
			RemainingServices: remainingActive,
			DeletedGroup:      false,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func resolveServicesToRemove(pg *v1alpha1.PreviewGroup, members []MemberRecord, opts LeaveOptions) map[string]bool {
	remove := make(map[string]bool)
	for _, name := range opts.ServiceNames {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			remove[trimmed] = true
		}
	}
	envTarget := strings.TrimSpace(opts.EnvironmentName)
	projTarget := strings.TrimSpace(opts.Project)
	for _, m := range members {
		if envTarget != "" && m.EnvironmentName == envTarget {
			remove[m.Service] = true
			continue
		}
		if opts.MR > 0 && m.MR == opts.MR {
			if projTarget == "" || strings.EqualFold(strings.TrimSpace(m.Project), projTarget) {
				remove[m.Service] = true
			}
		}
	}
	// Only return service names actually present in pg.Spec.Services
	present := make(map[string]bool, len(pg.Spec.Services))
	for _, svc := range pg.Spec.Services {
		present[svc.Name] = true
	}
	for k := range remove {
		if !present[k] {
			delete(remove, k)
		}
	}
	return remove
}

// FindGroupsForPR returns all PreviewGroups where the given project/MR or environmentName is a recorded member.
func FindGroupsForPR(ctx context.Context, c client.Client, project string, mr int, envName string) ([]v1alpha1.PreviewGroup, error) {
	if c == nil {
		return nil, nil
	}
	var pgList v1alpha1.PreviewGroupList
	if err := c.List(ctx, &pgList); err != nil {
		if meta.IsNoMatchError(err) || runtime.IsNotRegisteredError(err) {
			return nil, nil
		}
		return nil, err
	}

	projClean := strings.TrimSpace(project)
	envClean := strings.TrimSpace(envName)
	var matched []v1alpha1.PreviewGroup
	for _, pg := range pgList.Items {
		if !pg.DeletionTimestamp.IsZero() {
			continue
		}
		for _, m := range ParseMembers(&pg) {
			if envClean != "" && m.EnvironmentName == envClean {
				matched = append(matched, pg)
				break
			}
			if mr > 0 && m.MR == mr && (projClean == "" || strings.EqualFold(strings.TrimSpace(m.Project), projClean)) {
				matched = append(matched, pg)
				break
			}
		}
	}
	return matched, nil
}

// SyncMemberEnvironment propagates updated container image and service settings from a reconciled PR/MR Environment
// into every composed PreviewGroup where that Environment/MR is already a member.
func SyncMemberEnvironment(ctx context.Context, c client.Client, env *v1alpha1.Environment) ([]string, error) {
	if c == nil || env == nil {
		return nil, nil
	}
	svcSpec, member, err := ServiceFromEnvironment(env)
	if err != nil {
		// Environment may not have an image yet; nothing to sync.
		return nil, nil
	}

	groups, err := FindGroupsForPR(ctx, c, env.Spec.Source.Project, env.Spec.Source.MR, env.Name)
	if err != nil {
		return nil, err
	}

	var synced []string
	var errs []error
	for _, pg := range groups {
		var groupDB *v1alpha1.EnvironmentDatabase
		if pg.Spec.Database != nil {
			groupDB = pg.Spec.Database.DeepCopy()
		}
		_, joinErr := Join(ctx, c, JoinOptions{
			GroupName:     pg.Name,
			Namespace:     pg.Namespace,
			Services:      []v1alpha1.PreviewGroupServiceSpec{svcSpec},
			Members:       []MemberRecord{member},
			GroupDatabase: groupDB,
			Traceparent:   env.Annotations["divergedev.com/traceparent"],
		})
		if joinErr != nil {
			errs = append(errs, fmt.Errorf("sync %s in PreviewGroup %s: %w", svcSpec.Name, pg.Name, joinErr))
			continue
		}
		synced = append(synced, pg.Name)
	}
	if len(errs) > 0 {
		return synced, fmt.Errorf("%v", errs)
	}
	return synced, nil
}

// DetachMemberByPR detaches a closed/merged PR/MR's services from all composed PreviewGroups it joined,
// automatically deleting any PreviewGroup whose last active service was removed.
func DetachMemberByPR(ctx context.Context, c client.Client, project string, mr int, envName string) ([]LeaveResult, error) {
	groups, err := FindGroupsForPR(ctx, c, project, mr, envName)
	if err != nil {
		return nil, err
	}
	var results []LeaveResult
	for _, pg := range groups {
		res, leaveErr := Leave(ctx, c, LeaveOptions{
			GroupName:       pg.Name,
			Namespace:       pg.Namespace,
			Project:         project,
			MR:              mr,
			EnvironmentName: envName,
		})
		if leaveErr != nil {
			return results, leaveErr
		}
		if res != nil && (len(res.RemovedServices) > 0 || res.DeletedGroup) {
			results = append(results, *res)
		}
	}
	return results, nil
}
