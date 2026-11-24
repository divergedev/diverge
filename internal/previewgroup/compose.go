package previewgroup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
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

	// AnnotationTTLRenewedAt records the RFC3339 timestamp when a PreviewGroup or Environment TTL
	// was last renewed or extended so expiry is anchored to max(CreatedAt, RenewedAt) + TTL.
	AnnotationTTLRenewedAt = "divergedev.com/ttl-renewed-at"

	// AnnotationAttachedPreviewGroup marks a standalone Environment whose traffic/workload is currently
	// represented inside a composed PreviewGroup (hibernating its duplicate standalone pods while attached).
	AnnotationAttachedPreviewGroup = "divergedev.com/attached-previewgroup"

	// AnnotationHibernated scales standalone Environment workloads to zero while attached to a PreviewGroup.
	AnnotationHibernated = "divergedev.com/hibernated"

	// AnnotationGroupHibernated records ("true" | "false") whether hibernation was initiated by attaching to a PreviewGroup,
	// so detaching never overrides an independent manual/idle hibernation state ("false").
	AnnotationGroupHibernated = "divergedev.com/group-hibernated"

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
	Service                    string `json:"service"`
	Namespace                  string `json:"namespace,omitempty"`
	Provider                   string `json:"provider,omitempty"`
	Project                    string `json:"project,omitempty"`
	MR                         int    `json:"mr,omitempty"`
	Branch                     string `json:"branch,omitempty"`
	EnvironmentName            string `json:"environmentName,omitempty"`
	EnvironmentNamespace       string `json:"environmentNamespace,omitempty"`
	EnvironmentResourceVersion string `json:"environmentResourceVersion,omitempty"`
	UpdatedAt                  string `json:"updatedAt,omitempty"`
}

// JoinOptions configures adding or updating one or more services inside a PreviewGroup.
type JoinOptions struct {
	GroupName               string
	Namespace               string // Optional fallback namespace for fake/namespaced clients
	Services                []v1alpha1.PreviewGroupServiceSpec
	Members                 []MemberRecord
	Source                  *v1alpha1.EnvironmentSource
	HeaderKey               string
	HeaderKeyExplicit       bool
	HeaderValue             string
	HeaderValueExplicit     bool
	ExternalURL             string
	TTL                     *time.Duration
	TTLExplicit             bool
	GroupDatabase           *v1alpha1.EnvironmentDatabase // Optional shared group-level database override
	ClusterRef              *v1alpha1.ClusterTargetRef
	ClusterRefExplicit      bool
	Owner                   string
	Traceparent             string
	ReplaceServices         bool // True for `preview create --upsert` (replaces full service list)
	AllowTakeover           bool // True (`--force`) to allow replacing a service owned by a different MR/env
	SyncOnlyIfMember        bool // True (`SyncMemberEnvironment`) to skip if group or member record no longer exists
	PruneSameMR             bool // True (`ci-preview.sh`) to prune prior services from the same (Project, MR) absent in Services
	PreserveServiceDatabase bool // True to retain an existing per-service Database override when incoming Database is nil
}

// JoinResult summarizes a Join operation.
type JoinResult struct {
	PreviewGroup *v1alpha1.PreviewGroup
	Created      bool
	DeletedGroup bool
	Members      []MemberRecord
}

// LeaveOptions configures detaching services or a PR/MR from a PreviewGroup.
type LeaveOptions struct {
	GroupName            string
	Namespace            string
	ServiceNames         []string
	Provider             string
	Project              string
	MR                   int
	EnvironmentName      string // "<name>" or "<namespace>/<name>"
	EnvironmentNamespace string
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
	if pg == nil {
		return nil
	}
	return ParseMembersFromAnnotations(pg.Annotations)
}

// ParseMembersFromAnnotations reads the MemberRecord list from an annotations map.
func ParseMembersFromAnnotations(annotations map[string]string) []MemberRecord {
	if annotations == nil {
		return nil
	}
	raw := strings.TrimSpace(annotations[AnnotationGroupMembers])
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

// EffectiveTTLAnchor returns max(createdAt, annotation["divergedev.com/ttl-renewed-at"]).
func EffectiveTTLAnchor(createdAt *metav1.Time, annotations map[string]string) time.Time {
	var anchor time.Time
	if createdAt != nil {
		anchor = createdAt.Time
	}
	if len(annotations) > 0 {
		if raw := strings.TrimSpace(annotations[AnnotationTTLRenewedAt]); raw != "" {
			if parsed, err := time.Parse(time.RFC3339, raw); err == nil && parsed.After(anchor) {
				anchor = parsed
			} else if parsedNano, errNano := time.Parse(time.RFC3339Nano, raw); errNano == nil && parsedNano.After(anchor) {
				anchor = parsedNano
			}
		}
	}
	return anchor
}

func knownSCMProvider(p string) string {
	trimmed := strings.ToLower(strings.TrimSpace(p))
	if trimmed == "" || trimmed == "local" {
		return ""
	}
	return trimmed
}

// memberMatchesSCMProvider compares a recorded member's provider with a requesting webhook/CLI provider.
// When both sides specify a known SCM provider ("github" or "gitlab"), they must match so identical
// org/repo#N paths across providers cannot collide. Members recorded without a known SCM provider
// (empty or "local", e.g. legacy/CLI joins without --provider) still match on (Project, MR) for
// backwards compatibility.
func memberMatchesSCMProvider(memberProvider, reqProvider string) bool {
	req := knownSCMProvider(reqProvider)
	mem := knownSCMProvider(memberProvider)
	if req != "" && mem != "" && req != mem {
		return false
	}
	return true
}

// HasPRMember reports whether pg still tracks an active non-baseline service contributed by (provider, project, mr).
func HasPRMember(pg *v1alpha1.PreviewGroup, provider, project string, mr int) bool {
	if pg == nil || !pg.DeletionTimestamp.IsZero() || mr <= 0 {
		return false
	}
	active := make(map[string]bool, len(pg.Spec.Services))
	for _, svc := range pg.Spec.Services {
		if svc.Mode != v1alpha1.ServiceModeBaseline {
			active[svc.Name] = true
		}
	}
	projClean := strings.TrimSpace(project)
	for _, m := range ParseMembers(pg) {
		if !active[m.Service] {
			continue
		}
		if m.MR == mr &&
			memberMatchesSCMProvider(m.Provider, provider) &&
			(projClean == "" || strings.EqualFold(strings.TrimSpace(m.Project), projClean)) {
			return true
		}
	}
	return false
}

// SameReferencedEnvironment reports whether two MemberRecords refer to the exact same standalone Environment.
func SameReferencedEnvironment(existing, incoming MemberRecord) bool {
	exEnv := strings.TrimSpace(existing.EnvironmentName)
	inEnv := strings.TrimSpace(incoming.EnvironmentName)
	if exEnv == "" || inEnv == "" || exEnv != inEnv {
		return false
	}
	exNS := strings.TrimSpace(existing.EnvironmentNamespace)
	if exNS == "" {
		exNS = strings.TrimSpace(existing.Namespace)
	}
	inNS := strings.TrimSpace(incoming.EnvironmentNamespace)
	if inNS == "" {
		inNS = strings.TrimSpace(incoming.Namespace)
	}
	if exNS != "" && inNS != "" {
		return exNS == inNS
	}
	return true
}

// MemberMatchesEnvironmentSource verifies that a MemberRecord carries sufficient SCM or local branch
// identity matching a fetched standalone Environment's Spec.Source before claiming or hibernating that Environment.
func MemberMatchesEnvironmentSource(m MemberRecord, env *v1alpha1.Environment) bool {
	if env == nil {
		return false
	}
	mProv := knownSCMProvider(m.Provider)
	eProv := knownSCMProvider(env.Spec.Source.Provider)
	mProj := strings.TrimSpace(m.Project)
	eProj := strings.TrimSpace(env.Spec.Source.Project)
	mBranch := strings.TrimSpace(m.Branch)
	eBranch := strings.TrimSpace(env.Spec.Source.Branch)

	if eProv != "" || eProj != "" || env.Spec.Source.MR > 0 {
		if eProv != "" {
			if mProv != eProv {
				return false
			}
		} else if mProv != "" {
			return false
		}
		if eProj != "" {
			if mProj == "" || !strings.EqualFold(mProj, eProj) {
				return false
			}
		} else if mProj != "" {
			return false
		}
		if env.Spec.Source.MR > 0 {
			if m.MR != env.Spec.Source.MR {
				return false
			}
		} else if m.MR > 0 {
			return false
		}
		return true
	}

	if mProv != "" || mProj != "" || m.MR > 0 {
		return false
	}
	return eBranch != "" && mBranch != "" && mBranch == eBranch
}

// SameMemberOwner reports whether existing and incoming MemberRecords represent the same owning PR/MR or Environment.
// If existing has no distinct ownership metadata (e.g. manual service without MR or EnvironmentName), updates are allowed.
func SameMemberOwner(existing, incoming MemberRecord) bool {
	exProv := knownSCMProvider(existing.Provider)
	inProv := knownSCMProvider(incoming.Provider)
	if exProv != "" && inProv != "" && exProv != inProv {
		return false
	}

	exEnv := strings.TrimSpace(existing.EnvironmentName)
	inEnv := strings.TrimSpace(incoming.EnvironmentName)
	exEnvNS := strings.TrimSpace(existing.EnvironmentNamespace)
	if exEnvNS == "" {
		exEnvNS = strings.TrimSpace(existing.Namespace)
	}
	inEnvNS := strings.TrimSpace(incoming.EnvironmentNamespace)
	if inEnvNS == "" {
		inEnvNS = strings.TrimSpace(incoming.Namespace)
	}

	exProj := strings.ToLower(strings.TrimSpace(existing.Project))
	inProj := strings.ToLower(strings.TrimSpace(incoming.Project))

	if exProj != "" && existing.MR > 0 && inProj != "" && incoming.MR > 0 {
		return exProj == inProj && existing.MR == incoming.MR
	}
	if existing.MR > 0 && incoming.MR > 0 && existing.MR != incoming.MR {
		return false
	}
	if exEnv != "" && inEnv != "" {
		if exEnvNS != "" && inEnvNS != "" && exEnvNS != inEnvNS {
			return false
		}
		return exEnv == inEnv
	}
	// Existing belongs to a tracked MR (with project) or Environment, while incoming does not match it
	if (exProj != "" && existing.MR > 0) || exEnv != "" {
		return false
	}
	// Fallback when existing had neither Project+MR nor EnvironmentName: check MR and Namespace if set
	if existing.MR > 0 && incoming.MR != existing.MR {
		return false
	}
	if strings.TrimSpace(existing.Namespace) != "" && strings.TrimSpace(incoming.Namespace) != "" &&
		strings.TrimSpace(existing.Namespace) != strings.TrimSpace(incoming.Namespace) {
		return false
	}
	return true
}

// FormatOwnerSummary formats a human-readable owner string for collision diagnostics.
func FormatOwnerSummary(m MemberRecord) string {
	if strings.TrimSpace(m.Project) != "" && m.MR > 0 {
		return fmt.Sprintf("%s#%d", strings.TrimSpace(m.Project), m.MR)
	}
	if strings.TrimSpace(m.EnvironmentName) != "" {
		ns := strings.TrimSpace(m.EnvironmentNamespace)
		if ns == "" {
			ns = strings.TrimSpace(m.Namespace)
		}
		if ns != "" {
			return fmt.Sprintf("environment %s/%s", ns, strings.TrimSpace(m.EnvironmentName))
		}
		return fmt.Sprintf("environment %s", strings.TrimSpace(m.EnvironmentName))
	}
	if m.MR > 0 {
		return fmt.Sprintf("MR #%d", m.MR)
	}
	if strings.TrimSpace(m.Namespace) != "" {
		return fmt.Sprintf("namespace %s", strings.TrimSpace(m.Namespace))
	}
	return "another member"
}

// IsStaleResourceVersion reports whether incomingRV is strictly older (numerically) than existingRV.
func IsStaleResourceVersion(existingRV, incomingRV string) bool {
	if strings.TrimSpace(existingRV) == "" || strings.TrimSpace(incomingRV) == "" {
		return false
	}
	exNum, exErr := strconv.ParseUint(strings.TrimSpace(existingRV), 10, 64)
	inNum, inErr := strconv.ParseUint(strings.TrimSpace(incomingRV), 10, 64)
	if exErr != nil || inErr != nil {
		return false
	}
	return inNum < exNum
}

func isStaleResourceVersion(existingRV, incomingRV string) bool {
	return IsStaleResourceVersion(existingRV, incomingRV)
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

func resolveEnvironmentServiceName(env *v1alpha1.Environment) string {
	if env == nil {
		return ""
	}
	if env.Spec.ServiceConfig != nil && strings.TrimSpace(env.Spec.ServiceConfig.ServiceName) != "" {
		return strings.TrimSpace(env.Spec.ServiceConfig.ServiceName)
	}
	for _, cs := range env.Spec.Deploy.ChangedServices {
		if trimmed := strings.TrimSpace(cs); trimmed != "" {
			return trimmed
		}
	}
	if env.Labels != nil {
		if svc := strings.TrimSpace(env.Labels["divergedev.com/service"]); svc != "" {
			return svc
		}
	}
	return strings.TrimSpace(env.Name)
}

// ServiceFromEnvironment extracts a PreviewGroupServiceSpec and MemberRecord from an Environment CR.
// By default it preserves the Environment's per-service Database settings when non-empty.
func ServiceFromEnvironment(env *v1alpha1.Environment) (v1alpha1.PreviewGroupServiceSpec, MemberRecord, error) {
	if env == nil {
		return v1alpha1.PreviewGroupServiceSpec{}, MemberRecord{}, fmt.Errorf("environment must not be nil")
	}

	svcName := resolveEnvironmentServiceName(env)
	if svcName == "" {
		return v1alpha1.PreviewGroupServiceSpec{}, MemberRecord{}, fmt.Errorf("cannot determine service name from environment")
	}

	envNS := strings.TrimSpace(env.Namespace)
	if envNS == "" {
		envNS = "default"
	}
	targetNS := envNS
	if env.Spec.ServiceConfig != nil && strings.TrimSpace(env.Spec.ServiceConfig.Namespace) != "" {
		targetNS = strings.TrimSpace(env.Spec.ServiceConfig.Namespace)
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
		Service:                    svcName,
		Namespace:                  targetNS,
		Provider:                   env.Spec.Source.Provider,
		Project:                    env.Spec.Source.Project,
		MR:                         env.Spec.Source.MR,
		Branch:                     env.Spec.Source.Branch,
		EnvironmentName:            env.Name,
		EnvironmentNamespace:       envNS,
		EnvironmentResourceVersion: env.ResourceVersion,
		UpdatedAt:                  time.Now().UTC().Format(time.RFC3339),
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

func wakeStandaloneEnv(ctx context.Context, c client.Client, m MemberRecord) error {
	envName := strings.TrimSpace(m.EnvironmentName)
	if envName == "" || c == nil {
		return nil
	}
	envNS := strings.TrimSpace(m.EnvironmentNamespace)
	if envNS == "" {
		envNS = strings.TrimSpace(m.Namespace)
	}
	if envNS == "" {
		envNS = "default"
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var env v1alpha1.Environment
		if err := c.Get(ctx, types.NamespacedName{Name: envName, Namespace: envNS}, &env); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		if !env.DeletionTimestamp.IsZero() {
			return nil
		}
		if env.Labels != nil && env.Labels["divergedev.com/managed-by"] == "diverge-previewgroup" {
			return nil
		}
		// Wake only if attached to a PreviewGroup, preserving any independent manual/idle hibernation ("false").
		if env.Annotations == nil || strings.TrimSpace(env.Annotations[AnnotationAttachedPreviewGroup]) == "" {
			return nil
		}
		delete(env.Annotations, AnnotationAttachedPreviewGroup)
		if env.Annotations[AnnotationGroupHibernated] != "false" {
			delete(env.Annotations, AnnotationHibernated)
		}
		delete(env.Annotations, AnnotationGroupHibernated)
		return c.Update(ctx, &env)
	})
}

// Join creates a PreviewGroup or atomically merges/updates the specified services inside an existing PreviewGroup.
// By default each joining service keeps its own per-service Database settings unless opts.GroupDatabase is non-nil
// or the existing PreviewGroup already has a shared group-level Database configured.
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

	baseServices := make([]v1alpha1.PreviewGroupServiceSpec, len(opts.Services))
	for i := range opts.Services {
		svc := *opts.Services[i].DeepCopy()
		if svc.Mode == "" {
			svc.Mode = v1alpha1.ServiceModeImage
		}
		if opts.GroupDatabase != nil {
			svc.Database = nil
		}
		baseServices[i] = svc
	}

	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)
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
	for _, svc := range baseServices {
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
			if opts.SyncOnlyIfMember {
				result = nil
				return nil
			}
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

			servicesCopy := make([]v1alpha1.PreviewGroupServiceSpec, len(baseServices))
			for i := range baseServices {
				servicesCopy[i] = *baseServices[i].DeepCopy()
			}

			annotations := map[string]string{
				AnnotationTTLRenewedAt: nowStr,
			}
			if strings.TrimSpace(opts.Traceparent) != "" {
				annotations["divergedev.com/traceparent"] = strings.TrimSpace(opts.Traceparent)
			}

			pg := &v1alpha1.PreviewGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: groupName,
					Labels: map[string]string{
						LabelComposedGroup: "true",
					},
					Annotations: annotations,
				},
				Spec: v1alpha1.PreviewGroupSpec{
					Source: source,
					Routing: v1alpha1.PreviewGroupRouting{
						Mode:        "header",
						HeaderKey:   headerKey,
						HeaderValue: headerValue,
						ExternalURL: strings.TrimSpace(opts.ExternalURL),
					},
					Services: servicesCopy,
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

		if !existing.DeletionTimestamp.IsZero() && opts.SyncOnlyIfMember {
			result = nil
			return nil
		}

		priorMembers := ParseMembers(existing)
		priorByService := make(map[string]MemberRecord, len(priorMembers))
		for _, pm := range priorMembers {
			if strings.TrimSpace(pm.Service) != "" {
				priorByService[pm.Service] = pm
			}
		}

		if opts.SyncOnlyIfMember {
			// Verify the member is still actively present in this PreviewGroup and not superseded by a newer revision.
			allPresent := true
			for _, incSvc := range baseServices {
				incMember := membersByService[incSvc.Name]
				curMember, ok := priorByService[incSvc.Name]
				if !ok || !SameMemberOwner(curMember, incMember) {
					allPresent = false
					break
				}
				if isStaleResourceVersion(curMember.EnvironmentResourceVersion, incMember.EnvironmentResourceVersion) {
					allPresent = false
					break
				}
			}
			if !allPresent {
				result = nil
				return nil
			}
		}

		updated := existing.DeepCopy()
		if updated.Labels == nil {
			updated.Labels = make(map[string]string)
		}
		if updated.Annotations == nil {
			updated.Annotations = make(map[string]string)
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

		if opts.HeaderValueExplicit && strings.TrimSpace(opts.HeaderValue) != "" {
			updated.Spec.Routing.HeaderValue = strings.TrimSpace(opts.HeaderValue)
		} else if strings.TrimSpace(updated.Spec.Routing.HeaderValue) == "" {
			hv := strings.TrimSpace(opts.HeaderValue)
			if hv == "" {
				hv = groupName
			}
			updated.Spec.Routing.HeaderValue = hv
		}

		if strings.TrimSpace(opts.ExternalURL) != "" {
			updated.Spec.Routing.ExternalURL = strings.TrimSpace(opts.ExternalURL)
		}

		if opts.TTL != nil && *opts.TTL > 0 {
			if updated.Spec.Lifecycle == nil {
				updated.Spec.Lifecycle = &v1alpha1.PreviewGroupLifecycle{}
			}
			candidateExpiry := now.Add(*opts.TTL)
			var currentExpiry time.Time
			if updated.Spec.Lifecycle.TTL != nil {
				anchor := EffectiveTTLAnchor(updated.Status.CreatedAt, updated.Annotations)
				if anchor.IsZero() && !updated.CreationTimestamp.IsZero() {
					anchor = updated.CreationTimestamp.Time
				}
				if !anchor.IsZero() {
					currentExpiry = anchor.Add(updated.Spec.Lifecycle.TTL.Duration)
				}
			}
			// Always renew from `now` when explicitly passed or when extending beyond current expiry;
			// never shrink an existing group's remaining lifetime from an implicit CI default TTL.
			if opts.TTLExplicit || opts.ReplaceServices || currentExpiry.IsZero() || candidateExpiry.After(currentExpiry) {
				updated.Spec.Lifecycle.TTL = &metav1.Duration{Duration: *opts.TTL}
				updated.Annotations[AnnotationTTLRenewedAt] = nowStr
			}
		}

		if opts.GroupDatabase != nil {
			updated.Spec.Database = opts.GroupDatabase.DeepCopy()
		}
		hasActiveGroupDB := updated.Spec.Database != nil && !IsZeroDatabase(*updated.Spec.Database)

		if opts.ClusterRefExplicit {
			if opts.ClusterRef != nil {
				updated.Spec.ClusterRef = opts.ClusterRef.DeepCopy()
			} else {
				updated.Spec.ClusterRef = nil
			}
		} else if opts.ClusterRef != nil {
			updated.Spec.ClusterRef = opts.ClusterRef.DeepCopy()
		}

		if strings.TrimSpace(opts.Traceparent) != "" {
			updated.Annotations["divergedev.com/traceparent"] = strings.TrimSpace(opts.Traceparent)
		}

		servicesAttempt := make([]v1alpha1.PreviewGroupServiceSpec, len(baseServices))
		for i := range baseServices {
			cp := *baseServices[i].DeepCopy()
			if hasActiveGroupDB {
				cp.Database = nil
			}
			servicesAttempt[i] = cp
		}

		if opts.ReplaceServices {
			updated.Spec.Services = servicesAttempt
			if opts.Source != nil {
				src := *opts.Source
				src.Provider = normalizeProvider(src.Provider)
				if strings.TrimSpace(src.Branch) != "" {
					updated.Spec.Source = src
				}
			}
			priorMembers = nil
		} else {
			merged := slices.Clone(updated.Spec.Services)
			incomingNameSet := make(map[string]bool, len(servicesAttempt))
			for _, incoming := range servicesAttempt {
				incomingNameSet[incoming.Name] = true
				idx := slices.IndexFunc(merged, func(cur v1alpha1.PreviewGroupServiceSpec) bool {
					return cur.Name == incoming.Name
				})
				if idx >= 0 {
					if !opts.AllowTakeover {
						if curOwner, owned := priorByService[incoming.Name]; owned {
							incMember := membersByService[incoming.Name]
							if !opts.SyncOnlyIfMember && !SameMemberOwner(curOwner, incMember) {
								return fmt.Errorf(
									"service %q in PreviewGroup %q is already owned by %s; pass --force to replace it",
									incoming.Name, groupName, FormatOwnerSummary(curOwner),
								)
							}
							if !opts.SyncOnlyIfMember && SameReferencedEnvironment(curOwner, incMember) &&
								IsStaleResourceVersion(curOwner.EnvironmentResourceVersion, incMember.EnvironmentResourceVersion) {
								return fmt.Errorf(
									"environment %s resourceVersion %s is older than PreviewGroup %q member revision %s; refresh and retry or pass --force",
									formatEnvSelector(incMember.EnvironmentNamespace, incMember.EnvironmentName),
									incMember.EnvironmentResourceVersion,
									groupName,
									curOwner.EnvironmentResourceVersion,
								)
							}
						}
					}
					if opts.PreserveServiceDatabase && !hasActiveGroupDB && incoming.Database == nil && merged[idx].Database != nil {
						incoming.Database = merged[idx].Database.DeepCopy()
					}
					if incoming.ClusterRef == nil && !opts.ClusterRefExplicit && merged[idx].ClusterRef != nil {
						incoming.ClusterRef = merged[idx].ClusterRef.DeepCopy()
					}
					if incoming.PostDeploy == nil && merged[idx].PostDeploy != nil {
						incoming.PostDeploy = merged[idx].PostDeploy.DeepCopy()
					}
					merged[idx] = incoming
				} else {
					merged = append(merged, incoming)
				}
			}

			// Prune services previously contributed by the exact same (Provider, Project, MR) when reverted in a new commit.
			if opts.PruneSameMR && opts.Source != nil && strings.TrimSpace(opts.Source.Project) != "" && opts.Source.MR > 0 {
				sameMR := MemberRecord{
					Provider: opts.Source.Provider,
					Project:  opts.Source.Project,
					MR:       opts.Source.MR,
				}
				pruned := make([]v1alpha1.PreviewGroupServiceSpec, 0, len(merged))
				for _, cur := range merged {
					if !incomingNameSet[cur.Name] {
						if curOwner, ok := priorByService[cur.Name]; ok &&
							memberMatchesSCMProvider(curOwner.Provider, sameMR.Provider) &&
							strings.EqualFold(strings.TrimSpace(curOwner.Project), strings.TrimSpace(sameMR.Project)) &&
							curOwner.MR == sameMR.MR {
							continue
						}
					}
					pruned = append(pruned, cur)
				}
				merged = pruned
			}

			// If group-level shared DB was explicitly enabled on this Join, clear per-service DB across all retained services too.
			if opts.GroupDatabase != nil {
				for i := range merged {
					merged[i].Database = nil
				}
			}

			updated.Spec.Services = merged
		}

		// If pruning left zero non-baseline services, delete the PreviewGroup cleanly with UID/RV preconditions.
		activeCount := 0
		for _, s := range updated.Spec.Services {
			if s.Mode != v1alpha1.ServiceModeBaseline {
				activeCount++
			}
		}
		if activeCount == 0 {
			var delOpts []client.DeleteOption
			if existing.UID != "" || existing.ResourceVersion != "" {
				precond := &metav1.Preconditions{}
				if existing.UID != "" {
					uid := existing.UID
					precond.UID = &uid
				}
				if existing.ResourceVersion != "" {
					rv := existing.ResourceVersion
					precond.ResourceVersion = &rv
				}
				delOpts = append(delOpts, &client.DeleteOptions{Preconditions: precond})
			}
			if delErr := c.Delete(ctx, existing, delOpts...); delErr != nil && !apierrors.IsNotFound(delErr) {
				return delErr
			}
			result = &JoinResult{
				PreviewGroup: existing,
				DeletedGroup: true,
			}
			return nil
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

// SplitEnvironmentRef splits "<namespace>/<name>" or "<name>" into (namespace, name).
func SplitEnvironmentRef(raw string) (ns, name string) {
	trimmed := strings.TrimSpace(raw)
	if slash := strings.Index(trimmed, "/"); slash >= 0 {
		return strings.TrimSpace(trimmed[:slash]), strings.TrimSpace(trimmed[slash+1:])
	}
	return "", trimmed
}

func matchesEnvironmentMember(m MemberRecord, targetEnvNS, targetEnvName string) bool {
	if targetEnvName == "" || m.EnvironmentName != targetEnvName {
		return false
	}
	if targetEnvNS == "" {
		return true
	}
	memberEnvNS := strings.TrimSpace(m.EnvironmentNamespace)
	if memberEnvNS != "" {
		return memberEnvNS == targetEnvNS
	}
	if strings.TrimSpace(m.Namespace) != "" {
		return strings.TrimSpace(m.Namespace) == targetEnvNS
	}
	return false
}

// Leave detaches matching services from a PreviewGroup.
// When zero non-baseline services remain in the group, the PreviewGroup itself is automatically deleted
// with optimistic concurrency preconditions so concurrent Joins are never lost.
func Leave(ctx context.Context, c client.Client, opts LeaveOptions) (*LeaveResult, error) {
	if c == nil {
		return nil, fmt.Errorf("kubernetes client is required")
	}
	groupName, err := NormalizeGroupName(opts.GroupName)
	if err != nil {
		return nil, err
	}

	var detachedMembers []MemberRecord
	var result *LeaveResult
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		detachedMembers = nil
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

		var keptMembers []MemberRecord
		for _, m := range members {
			if toRemove[m.Service] {
				detachedMembers = append(detachedMembers, m)
			} else {
				keptMembers = append(keptMembers, m)
			}
		}

		// Auto-delete the PreviewGroup when the last active (non-baseline) service leaves,
		// guarded by UID & ResourceVersion preconditions so a concurrent Join triggers retry instead of data loss.
		if len(remainingActive) == 0 {
			var delOpts []client.DeleteOption
			if pg.UID != "" || pg.ResourceVersion != "" {
				precond := &metav1.Preconditions{}
				if pg.UID != "" {
					uid := pg.UID
					precond.UID = &uid
				}
				if pg.ResourceVersion != "" {
					rv := pg.ResourceVersion
					precond.ResourceVersion = &rv
				}
				delOpts = append(delOpts, &client.DeleteOptions{Preconditions: precond})
			}
			if delErr := c.Delete(ctx, pg, delOpts...); delErr != nil {
				if apierrors.IsNotFound(delErr) {
					result = &LeaveResult{
						GroupName:       pg.Name,
						RemovedServices: removedNames,
						DeletedGroup:    true,
					}
					return nil
				}
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
		if err := SetMembers(updated, keptMembers); err != nil {
			return err
		}

		// If the group's primary Spec.Source belonged to a detached member, promote the first remaining MR member
		// or clear back to local source when only non-MR services remain.
		if updated.Spec.Source.MR > 0 {
			stillPresent := false
			for _, km := range keptMembers {
				if km.MR == updated.Spec.Source.MR &&
					memberMatchesSCMProvider(km.Provider, updated.Spec.Source.Provider) &&
					(updated.Spec.Source.Project == "" || strings.EqualFold(strings.TrimSpace(km.Project), strings.TrimSpace(updated.Spec.Source.Project))) {
					stillPresent = true
					break
				}
			}
			if !stillPresent {
				var promoted *MemberRecord
				for i := range keptMembers {
					if keptMembers[i].MR > 0 && strings.TrimSpace(keptMembers[i].Project) != "" {
						promoted = &keptMembers[i]
						break
					}
				}
				if promoted == nil {
					for i := range keptMembers {
						if keptMembers[i].MR > 0 {
							promoted = &keptMembers[i]
							break
						}
					}
				}
				if promoted != nil {
					branch := strings.TrimSpace(promoted.Branch)
					if branch == "" {
						branch = updated.Name
					}
					updated.Spec.Source = v1alpha1.EnvironmentSource{
						Provider: normalizeProvider(promoted.Provider),
						Project:  promoted.Project,
						MR:       promoted.MR,
						Branch:   branch,
					}
				} else {
					updated.Spec.Source = v1alpha1.EnvironmentSource{
						Provider: "local",
						Branch:   updated.Name,
					}
				}
			}
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

	// Also recover any standalone PR Environment still carrying AnnotationAttachedPreviewGroup == groupName
	// from an earlier partial Leave whose membership removal committed before the Environment wake succeeded.
	recovered, recErr := collectOrphanedAttachedMembers(ctx, c, groupName, opts)
	if recErr != nil {
		return nil, recErr
	}
	wakeCandidates := make([]MemberRecord, 0, len(detachedMembers)+len(recovered))
	seenWake := make(map[string]bool, len(detachedMembers)+len(recovered))
	for _, cand := range append(detachedMembers, recovered...) {
		envName := strings.TrimSpace(cand.EnvironmentName)
		if envName == "" {
			continue
		}
		envNS := strings.TrimSpace(cand.EnvironmentNamespace)
		if envNS == "" {
			envNS = strings.TrimSpace(cand.Namespace)
		}
		if envNS == "" {
			envNS = "default"
		}
		key := envNS + "/" + envName
		if seenWake[key] {
			continue
		}
		seenWake[key] = true
		cand.EnvironmentName = envName
		cand.EnvironmentNamespace = envNS
		wakeCandidates = append(wakeCandidates, cand)
	}

	// Wake any standalone PR Environment whose exact (namespace, name) is no longer claimed by any PreviewGroup.
	for _, dm := range wakeCandidates {
		claimed, fErr := isEnvironmentClaimedByAnyGroup(ctx, c, dm.EnvironmentNamespace, dm.EnvironmentName)
		if fErr != nil {
			return nil, fErr
		}
		if !claimed {
			if wErr := wakeStandaloneEnv(ctx, c, dm); wErr != nil {
				return nil, wErr
			}
		}
	}

	return result, nil
}

func isEnvironmentClaimedByAnyGroup(ctx context.Context, c client.Client, envNS, envName string) (bool, error) {
	if c == nil || strings.TrimSpace(envName) == "" {
		return false, nil
	}
	var pgList v1alpha1.PreviewGroupList
	if err := c.List(ctx, &pgList); err != nil {
		if meta.IsNoMatchError(err) || runtime.IsNotRegisteredError(err) {
			return false, nil
		}
		return false, err
	}
	for i := range pgList.Items {
		pg := &pgList.Items[i]
		if !pg.DeletionTimestamp.IsZero() {
			continue
		}
		for _, m := range ParseMembers(pg) {
			if matchesEnvironmentMember(m, envNS, envName) {
				return true, nil
			}
		}
	}
	return false, nil
}

func collectOrphanedAttachedMembers(ctx context.Context, c client.Client, groupName string, opts LeaveOptions) ([]MemberRecord, error) {
	if c == nil || strings.TrimSpace(groupName) == "" {
		return nil, nil
	}
	envNS := strings.TrimSpace(opts.EnvironmentNamespace)
	envTarget := strings.TrimSpace(opts.EnvironmentName)
	if parsedNS, parsedName := SplitEnvironmentRef(envTarget); parsedNS != "" {
		if envNS == "" {
			envNS = parsedNS
		}
		envTarget = parsedName
	}

	var out []MemberRecord
	if envTarget != "" && envNS != "" {
		var env v1alpha1.Environment
		if err := c.Get(ctx, types.NamespacedName{Namespace: envNS, Name: envTarget}, &env); err != nil {
			if apierrors.IsNotFound(err) || apierrors.IsForbidden(err) || meta.IsNoMatchError(err) || runtime.IsNotRegisteredError(err) {
				return nil, nil
			}
			return nil, err
		}
		if env.Annotations[AnnotationAttachedPreviewGroup] == groupName {
			out = append(out, MemberRecord{EnvironmentName: env.Name, EnvironmentNamespace: env.Namespace})
		}
		return out, nil
	}

	var listOpts []client.ListOption
	if envNS != "" {
		listOpts = append(listOpts, client.InNamespace(envNS))
	} else if strings.TrimSpace(opts.Namespace) != "" {
		listOpts = append(listOpts, client.InNamespace(strings.TrimSpace(opts.Namespace)))
	}
	var envList v1alpha1.EnvironmentList
	if err := c.List(ctx, &envList, listOpts...); err != nil {
		if apierrors.IsForbidden(err) || meta.IsNoMatchError(err) || runtime.IsNotRegisteredError(err) {
			return nil, nil
		}
		return nil, err
	}
	projTarget := strings.TrimSpace(opts.Project)
	for i := range envList.Items {
		env := &envList.Items[i]
		if env.Annotations[AnnotationAttachedPreviewGroup] != groupName {
			continue
		}
		if envTarget != "" && env.Name == envTarget {
			out = append(out, MemberRecord{EnvironmentName: env.Name, EnvironmentNamespace: env.Namespace})
			continue
		}
		if opts.MR > 0 && env.Spec.Source.MR == opts.MR &&
			memberMatchesSCMProvider(env.Spec.Source.Provider, opts.Provider) &&
			(projTarget == "" || strings.EqualFold(strings.TrimSpace(env.Spec.Source.Project), projTarget)) {
			out = append(out, MemberRecord{EnvironmentName: env.Name, EnvironmentNamespace: env.Namespace})
			continue
		}
		if len(opts.ServiceNames) > 0 {
			resolvedSvcName := resolveEnvironmentServiceName(env)
			for _, svcName := range opts.ServiceNames {
				svcClean := strings.TrimSpace(svcName)
				if svcClean != "" && resolvedSvcName == svcClean {
					out = append(out, MemberRecord{EnvironmentName: env.Name, EnvironmentNamespace: env.Namespace})
					break
				}
			}
		}
	}
	return out, nil
}

func formatEnvSelector(envNS, envName string) string {
	if strings.TrimSpace(envNS) != "" {
		return strings.TrimSpace(envNS) + "/" + strings.TrimSpace(envName)
	}
	return strings.TrimSpace(envName)
}

func resolveServicesToRemove(pg *v1alpha1.PreviewGroup, members []MemberRecord, opts LeaveOptions) map[string]bool {
	remove := make(map[string]bool)
	for _, name := range opts.ServiceNames {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			remove[trimmed] = true
		}
	}
	envNS := strings.TrimSpace(opts.EnvironmentNamespace)
	envTarget := strings.TrimSpace(opts.EnvironmentName)
	if parsedNS, parsedName := SplitEnvironmentRef(envTarget); parsedNS != "" {
		if envNS == "" {
			envNS = parsedNS
		}
		envTarget = parsedName
	}

	projTarget := strings.TrimSpace(opts.Project)
	for _, m := range members {
		if matchesEnvironmentMember(m, envNS, envTarget) {
			remove[m.Service] = true
			continue
		}
		if opts.MR > 0 && m.MR == opts.MR {
			if memberMatchesSCMProvider(m.Provider, opts.Provider) &&
				(projTarget == "" || strings.EqualFold(strings.TrimSpace(m.Project), projTarget)) {
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

// FindGroupsForPR returns all PreviewGroups where the given provider/project/MR or environmentName ("<name>" or "<ns>/<name>") is a recorded member.
// Authorization errors (403 Forbidden) are returned to callers so destructive commands can distinguish RBAC denial from an empty list.
func FindGroupsForPR(ctx context.Context, c client.Client, provider, project string, mr int, envRef string) ([]v1alpha1.PreviewGroup, error) {
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
	envNS, envClean := SplitEnvironmentRef(envRef)
	var matched []v1alpha1.PreviewGroup
	for _, pg := range pgList.Items {
		if !pg.DeletionTimestamp.IsZero() {
			continue
		}
		for _, m := range ParseMembers(&pg) {
			if matchesEnvironmentMember(m, envNS, envClean) {
				matched = append(matched, pg)
				break
			}
			if mr > 0 && m.MR == mr &&
				memberMatchesSCMProvider(m.Provider, provider) &&
				(projClean == "" || strings.EqualFold(strings.TrimSpace(m.Project), projClean)) {
				matched = append(matched, pg)
				break
			}
		}
	}
	return matched, nil
}

// FindGroupsForPROptional is like FindGroupsForPR, except 403 Forbidden (e.g. namespace-scoped RBAC installs)
// is treated as empty discovery for non-destructive / background callers.
func FindGroupsForPROptional(ctx context.Context, c client.Client, provider, project string, mr int, envRef string) ([]v1alpha1.PreviewGroup, error) {
	groups, err := FindGroupsForPR(ctx, c, provider, project, mr, envRef)
	if apierrors.IsForbidden(err) {
		return nil, nil
	}
	return groups, err
}

// SyncMemberEnvironment propagates updated container image and service settings from a reconciled PR/MR Environment
// into every composed PreviewGroup where that Environment/MR is already a member.
// Inside Join's retry loop, SyncOnlyIfMember verifies the group and member record still exist and inherits the live
// group-level database settings rather than relying on a pre-discovery snapshot.
func SyncMemberEnvironment(ctx context.Context, c client.Client, env *v1alpha1.Environment) ([]string, error) {
	if c == nil || env == nil {
		return nil, nil
	}
	svcSpec, member, err := ServiceFromEnvironment(env)
	if err != nil {
		// Environment may not have an image yet; nothing to sync.
		return nil, nil
	}

	envRef := formatEnvSelector(env.Namespace, env.Name)
	groups, err := FindGroupsForPROptional(ctx, c, env.Spec.Source.Provider, env.Spec.Source.Project, env.Spec.Source.MR, envRef)
	if err != nil {
		return nil, err
	}

	var synced []string
	var errs []error
	for _, pg := range groups {
		res, joinErr := Join(ctx, c, JoinOptions{
			GroupName:        pg.Name,
			Namespace:        pg.Namespace,
			Services:         []v1alpha1.PreviewGroupServiceSpec{svcSpec},
			Members:          []MemberRecord{member},
			SyncOnlyIfMember: true,
			Traceparent:      env.Annotations["divergedev.com/traceparent"],
		})
		if joinErr != nil {
			errs = append(errs, fmt.Errorf("sync %s in PreviewGroup %s: %w", svcSpec.Name, pg.Name, joinErr))
			continue
		}
		if res != nil && !res.DeletedGroup {
			synced = append(synced, pg.Name)
		}
	}
	if len(errs) > 0 {
		return synced, fmt.Errorf("%v", errs)
	}
	return synced, nil
}

// DetachMemberByPR detaches a closed/merged PR/MR's services from all composed PreviewGroups it joined,
// automatically deleting any PreviewGroup whose last active service was removed.
func DetachMemberByPR(ctx context.Context, c client.Client, provider, project string, mr int, envRef string) ([]LeaveResult, error) {
	groups, err := FindGroupsForPR(ctx, c, provider, project, mr, envRef)
	if err != nil {
		return nil, err
	}
	envNS, envName := SplitEnvironmentRef(envRef)
	var results []LeaveResult
	for _, pg := range groups {
		res, leaveErr := Leave(ctx, c, LeaveOptions{
			GroupName:            pg.Name,
			Namespace:            pg.Namespace,
			Provider:             provider,
			Project:              project,
			MR:                   mr,
			EnvironmentName:      envName,
			EnvironmentNamespace: envNS,
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
