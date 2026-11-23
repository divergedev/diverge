package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/protocgen/proto2mcp/pkg/mcpruntime"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/durationpb"

	divergev1alpha1 "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	divergev1alpha1connect "github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	"github.com/divergedev/diverge/internal/previewgroup"
)

const (
	joinPreviewGroupToolDescription  = "Create or join a shared multi-MR PreviewGroup by attaching existing PR/MR Environments or service images without clobbering other MRs' services. Preserves per-service database configs by default unless shared_database_mode is set."
	leavePreviewGroupToolDescription = "Detach services or PR/MR Environments from a shared PreviewGroup (falling back to baseline) and automatically delete the PreviewGroup when the last active service leaves."
)

var joinPreviewGroupSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"group_name": {
			"type": "string",
			"description": "Shared PreviewGroup name or Git branch slug (e.g. 'feat-checkout' or 'feat/checkout')"
		},
		"namespace": {
			"type": "string",
			"description": "Default Kubernetes namespace when resolving environment_names or service namespaces (default: 'default')"
		},
		"environment_names": {
			"type": "array",
			"items": {"type": "string"},
			"description": "Existing PR/MR Environment names ('<name>' or '<namespace>/<name>') to join into the PreviewGroup"
		},
		"services": {
			"type": "array",
			"description": "Explicit service definitions to join into the PreviewGroup",
			"items": {
				"type": "object",
				"properties": {
					"name": {"type": "string"},
					"image": {"type": "string"},
					"namespace": {"type": "string"},
					"port": {"type": "integer"},
					"mode": {"type": "string", "enum": ["image", "baseline"]},
					"project": {"type": "string"},
					"mr": {"type": "integer"},
					"branch": {"type": "string"}
				},
				"required": ["name"],
				"additionalProperties": false
			}
		},
		"header_key": {
			"type": "string",
			"description": "HTTP routing header key (default: 'x-diverge-env')"
		},
		"header_value": {
			"type": "string",
			"description": "HTTP routing header value (defaults to normalized group_name)"
		},
		"external_url": {
			"type": "string",
			"description": "Optional external preview URL or staging base URL"
		},
		"ttl_seconds": {
			"type": "integer",
			"description": "Optional TTL in seconds for the PreviewGroup (default: 86400 on create)"
		},
		"shared_database_mode": {
			"type": "string",
			"enum": ["shared", "schema", "snapshot", "fresh", "cnpg", "neon"],
			"description": "Optional shared group-level database mode; omit to preserve each MR's own database config"
		}
	},
	"required": ["group_name"],
	"additionalProperties": false
}`)

var leavePreviewGroupSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"group_name": {
			"type": "string",
			"description": "Shared PreviewGroup name to detach from"
		},
		"namespace": {
			"type": "string",
			"description": "Kubernetes namespace context (default: 'default')"
		},
		"service_names": {
			"type": "array",
			"items": {"type": "string"},
			"description": "Service name(s) to detach from the PreviewGroup"
		},
		"environment_names": {
			"type": "array",
			"items": {"type": "string"},
			"description": "Environment CR name(s) whose contributed service(s) should be detached"
		},
		"project": {
			"type": "string",
			"description": "Optional SCM project path (e.g. 'acme/payments-api') when detaching by mr"
		},
		"mr": {
			"type": "integer",
			"description": "Optional PR/MR number whose contributed service(s) should be detached"
		}
	},
	"required": ["group_name"],
	"additionalProperties": false
}`)

type mcpJoinServiceInput struct {
	Name      string `json:"name"`
	Image     string `json:"image"`
	Namespace string `json:"namespace"`
	Port      int32  `json:"port"`
	Mode      string `json:"mode"`
	Project   string `json:"project"`
	MR        int    `json:"mr"`
	Branch    string `json:"branch"`
}

func parseProtoMembers(annotations map[string]string) []previewgroup.MemberRecord {
	if len(annotations) == 0 {
		return nil
	}
	raw := strings.TrimSpace(annotations[previewgroup.AnnotationGroupMembers])
	if raw == "" {
		return nil
	}
	var out []previewgroup.MemberRecord
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func isZeroProtoDatabase(db *divergev1alpha1.EnvironmentDatabase) bool {
	if db == nil {
		return true
	}
	return strings.TrimSpace(db.Mode) == "" &&
		strings.TrimSpace(db.ConnectionRef) == "" &&
		strings.TrimSpace(db.SeedSource) == "" &&
		db.MigrationJob == nil &&
		db.Atlas == nil
}

func protoEnvToServiceSpec(env *divergev1alpha1.Environment, defaultNS string) (*divergev1alpha1.PreviewGroupServiceSpec, previewgroup.MemberRecord, error) {
	if env == nil {
		return nil, previewgroup.MemberRecord{}, errors.New("environment is nil")
	}
	spec := env.GetSpec()
	sc := spec.GetServiceConfig()

	svcName := strings.TrimSpace(sc.GetServiceName())
	if svcName == "" && spec.GetDeploy() != nil {
		for _, cs := range spec.GetDeploy().GetChangedServices() {
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
		return nil, previewgroup.MemberRecord{}, errors.New("cannot determine service name from environment")
	}

	image := strings.TrimSpace(sc.GetImage())
	if image == "" && env.Annotations != nil {
		image = strings.TrimSpace(env.Annotations["divergedev.com/image"])
	}
	if image == "" {
		return nil, previewgroup.MemberRecord{}, fmt.Errorf("environment %q has no container image configured", env.Name)
	}

	ns := strings.TrimSpace(sc.GetNamespace())
	if ns == "" {
		ns = strings.TrimSpace(env.Namespace)
	}
	if ns == "" {
		ns = defaultNS
	}

	pgSvc := &divergev1alpha1.PreviewGroupServiceSpec{
		Name:            svcName,
		Image:           image,
		Mode:            "image",
		Namespace:       ns,
		Port:            sc.GetPort(),
		ParentRef:       sc.GetParentRef(),
		PathPrefix:      sc.GetPathPrefix(),
		Protocol:        sc.GetProtocol(),
		ImagePullPolicy: sc.GetImagePullPolicy(),
		Env:             sc.GetEnv(),
		Resources:       sc.GetResources(),
		Keda:            sc.GetKeda(),
	}
	if spec.GetRouting() != nil {
		pgSvc.AsyncRoutes = spec.GetRouting().GetAsyncRoutes()
	}
	if !isZeroProtoDatabase(spec.GetDatabase()) {
		pgSvc.Database = spec.GetDatabase()
	}
	if spec.GetDeploy() != nil && spec.GetDeploy().GetClusterRef() != nil {
		pgSvc.ClusterRef = spec.GetDeploy().GetClusterRef()
	}

	src := spec.GetSource()
	member := previewgroup.MemberRecord{
		Service:         svcName,
		Namespace:       ns,
		Provider:        src.GetProvider(),
		Project:         src.GetProject(),
		MR:              int(src.GetMr()),
		Branch:          src.GetBranch(),
		EnvironmentName: env.Name,
		UpdatedAt:       time.Now().UTC().Format(time.RFC3339),
	}
	return pgSvc, member, nil
}

func registerPreviewGroupComposeTools(
	registry mcpruntime.Registry,
	envClient divergev1alpha1connect.EnvironmentServiceClient,
	pgClient divergev1alpha1connect.PreviewGroupServiceClient,
) {
	registry.Register(mcpruntime.ToolDefinition{
		Name:        "diverge_join_preview_group",
		Description: joinPreviewGroupToolDescription,
		InputSchema: joinPreviewGroupSchema,
	}, func(ctx context.Context, req mcpruntime.ToolRequest) (*mcpruntime.CallToolResult, error) {
		var params struct {
			GroupName          string                `json:"group_name"`
			Namespace          string                `json:"namespace"`
			EnvironmentNames   []string              `json:"environment_names"`
			Services           []mcpJoinServiceInput `json:"services"`
			HeaderKey          string                `json:"header_key"`
			HeaderValue        string                `json:"header_value"`
			ExternalURL        string                `json:"external_url"`
			TTLSeconds         int64                 `json:"ttl_seconds"`
			SharedDatabaseMode string                `json:"shared_database_mode"`
		}
		if len(req.Arguments) > 0 {
			if err := json.Unmarshal(req.Arguments, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
		}
		if pgClient == nil {
			return errorToolResult(errors.New("preview group client is not configured")), nil
		}
		groupName, err := previewgroup.NormalizeGroupName(params.GroupName)
		if err != nil {
			return errorToolResult(err), nil
		}
		if len(params.EnvironmentNames) == 0 && len(params.Services) == 0 {
			return errorToolResult(errors.New("provide at least one entry in environment_names or services")), nil
		}
		if err := validateSharedDBMode(params.SharedDatabaseMode); err != nil {
			return errorToolResult(err), nil
		}

		ns := strings.TrimSpace(params.Namespace)
		if ns == "" {
			ns = "default"
		}

		nowStr := time.Now().UTC().Format(time.RFC3339)
		var incomingSvcs []*divergev1alpha1.PreviewGroupServiceSpec
		membersBySvc := make(map[string]previewgroup.MemberRecord)
		var firstSource *divergev1alpha1.EnvironmentSource

		for _, envRef := range params.EnvironmentNames {
			if envClient == nil {
				return errorToolResult(errors.New("environment client is not configured")), nil
			}
			envNS := ns
			envName := strings.TrimSpace(envRef)
			if slash := strings.Index(envName, "/"); slash > 0 {
				envNS = strings.TrimSpace(envName[:slash])
				envName = strings.TrimSpace(envName[slash+1:])
			}
			if envName == "" {
				continue
			}
			envResp, getErr := envClient.GetEnvironment(ctx, connect.NewRequest(&divergev1alpha1.GetEnvironmentRequest{
				Name:      envName,
				Namespace: envNS,
			}))
			if getErr != nil {
				return errorToolResult(getErr), nil
			}
			svcSpec, member, convErr := protoEnvToServiceSpec(envResp.Msg.GetEnvironment(), ns)
			if convErr != nil {
				return errorToolResult(convErr), nil
			}
			if strings.TrimSpace(params.SharedDatabaseMode) != "" {
				svcSpec.Database = nil
			}
			incomingSvcs = append(incomingSvcs, svcSpec)
			membersBySvc[svcSpec.Name] = member
			if firstSource == nil && envResp.Msg.GetEnvironment().GetSpec().GetSource() != nil {
				firstSource = envResp.Msg.GetEnvironment().GetSpec().GetSource()
			}
		}

		for _, rawSvc := range params.Services {
			svcName := strings.TrimSpace(rawSvc.Name)
			if svcName == "" {
				return errorToolResult(errors.New("service name cannot be empty")), nil
			}
			mode := strings.ToLower(strings.TrimSpace(rawSvc.Mode))
			if mode == "" {
				mode = "image"
			}
			if mode == "image" && strings.TrimSpace(rawSvc.Image) == "" {
				return errorToolResult(fmt.Errorf("service %q requires an image when mode is 'image'", svcName)), nil
			}
			svcNS := strings.TrimSpace(rawSvc.Namespace)
			if svcNS == "" {
				svcNS = ns
			}
			incomingSvcs = append(incomingSvcs, &divergev1alpha1.PreviewGroupServiceSpec{
				Name:      svcName,
				Image:     strings.TrimSpace(rawSvc.Image),
				Mode:      mode,
				Namespace: svcNS,
				Port:      rawSvc.Port,
			})
			if mode != "baseline" {
				membersBySvc[svcName] = previewgroup.MemberRecord{
					Service:   svcName,
					Namespace: svcNS,
					Project:   strings.TrimSpace(rawSvc.Project),
					MR:        rawSvc.MR,
					Branch:    strings.TrimSpace(rawSvc.Branch),
					UpdatedAt: nowStr,
				}
			}
		}

		headerKey := strings.TrimSpace(params.HeaderKey)
		if headerKey == "" {
			headerKey = previewgroup.DefaultRoutingHeaderKey
		}
		headerVal := strings.TrimSpace(params.HeaderValue)
		if headerVal == "" {
			headerVal = groupName
		}

		var sharedDB *divergev1alpha1.EnvironmentDatabase
		if strings.TrimSpace(params.SharedDatabaseMode) != "" {
			sharedDB = &divergev1alpha1.EnvironmentDatabase{
				Mode: strings.ToLower(strings.TrimSpace(params.SharedDatabaseMode)),
			}
		}

		var finalPG *divergev1alpha1.PreviewGroup
		created := false
		for attempt := 0; attempt < 3; attempt++ {
			getResp, getErr := pgClient.GetPreviewGroup(ctx, connect.NewRequest(&divergev1alpha1.GetPreviewGroupRequest{
				Name:      groupName,
				Namespace: ns,
			}))
			if getErr != nil {
				if connect.CodeOf(getErr) != connect.CodeNotFound {
					return errorToolResult(getErr), nil
				}
				ttlSec := params.TTLSeconds
				if ttlSec <= 0 {
					ttlSec = 86400
				}
				src := &divergev1alpha1.EnvironmentSource{
					Provider: "local",
					Branch:   groupName,
				}
				if firstSource != nil && strings.TrimSpace(firstSource.Branch) != "" {
					prov := strings.ToLower(strings.TrimSpace(firstSource.Provider))
					if prov != "github" && prov != "gitlab" {
						prov = "local"
					}
					src = &divergev1alpha1.EnvironmentSource{
						Provider:  prov,
						Project:   firstSource.Project,
						Mr:        firstSource.Mr,
						Branch:    firstSource.Branch,
						CommitSha: firstSource.CommitSha,
					}
				}
				ordered := make([]previewgroup.MemberRecord, 0, len(membersBySvc))
				for _, s := range incomingSvcs {
					if m, ok := membersBySvc[s.Name]; ok {
						ordered = append(ordered, m)
					}
				}
				membersJSON, _ := json.Marshal(ordered)
				createResp, cErr := pgClient.CreatePreviewGroup(ctx, connect.NewRequest(&divergev1alpha1.CreatePreviewGroupRequest{
					Namespace: ns,
					PreviewGroup: &divergev1alpha1.PreviewGroup{
						Name:      groupName,
						Namespace: ns,
						Labels: map[string]string{
							previewgroup.LabelComposedGroup: "true",
						},
						Annotations: map[string]string{
							previewgroup.AnnotationGroupMembers: string(membersJSON),
						},
						Spec: &divergev1alpha1.PreviewGroupSpec{
							Source: src,
							Routing: &divergev1alpha1.PreviewGroupRouting{
								Mode:        "header",
								HeaderKey:   headerKey,
								HeaderValue: headerVal,
								ExternalUrl: strings.TrimSpace(params.ExternalURL),
							},
							Lifecycle: &divergev1alpha1.PreviewGroupLifecycle{
								Ttl:            durationpb.New(time.Duration(ttlSec) * time.Second),
								CleanupOnMerge: true,
							},
							Database: sharedDB,
							Services: incomingSvcs,
						},
					},
				}))
				if cErr != nil {
					if connect.CodeOf(cErr) == connect.CodeAlreadyExists {
						continue
					}
					return errorToolResult(cErr), nil
				}
				finalPG = createResp.Msg.GetPreviewGroup()
				created = true
				break
			}

			cur := getResp.Msg.GetPreviewGroup()
			spec := cur.GetSpec()
			if spec == nil {
				spec = &divergev1alpha1.PreviewGroupSpec{}
			}
			mergedSvcs := slices.Clone(spec.GetServices())
			for _, inc := range incomingSvcs {
				idx := slices.IndexFunc(mergedSvcs, func(s *divergev1alpha1.PreviewGroupServiceSpec) bool {
					return s.GetName() == inc.Name
				})
				if idx >= 0 {
					if inc.ClusterRef == nil && mergedSvcs[idx].GetClusterRef() != nil {
						inc.ClusterRef = mergedSvcs[idx].GetClusterRef()
					}
					mergedSvcs[idx] = inc
				} else {
					mergedSvcs = append(mergedSvcs, inc)
				}
			}
			spec.Services = mergedSvcs
			if spec.Routing == nil {
				spec.Routing = &divergev1alpha1.PreviewGroupRouting{
					Mode:        "header",
					HeaderKey:   headerKey,
					HeaderValue: headerVal,
				}
			}
			if strings.TrimSpace(params.HeaderKey) != "" {
				spec.Routing.HeaderKey = headerKey
			}
			if strings.TrimSpace(params.HeaderValue) != "" {
				spec.Routing.HeaderValue = headerVal
			}
			if strings.TrimSpace(params.ExternalURL) != "" {
				spec.Routing.ExternalUrl = strings.TrimSpace(params.ExternalURL)
			}
			if params.TTLSeconds > 0 {
				if spec.Lifecycle == nil {
					spec.Lifecycle = &divergev1alpha1.PreviewGroupLifecycle{}
				}
				spec.Lifecycle.Ttl = durationpb.New(time.Duration(params.TTLSeconds) * time.Second)
			}
			if sharedDB != nil {
				spec.Database = sharedDB
			}

			existingMembers := parseProtoMembers(cur.GetAnnotations())
			byService := make(map[string]previewgroup.MemberRecord, len(existingMembers)+len(membersBySvc))
			for _, m := range existingMembers {
				byService[m.Service] = m
			}
			for k, m := range membersBySvc {
				byService[k] = m
			}
			var ordered []previewgroup.MemberRecord
			for _, s := range mergedSvcs {
				if s.GetMode() == "baseline" {
					continue
				}
				if m, ok := byService[s.GetName()]; ok {
					ordered = append(ordered, m)
				}
			}
			ann := make(map[string]string, len(cur.GetAnnotations())+1)
			for k, v := range cur.GetAnnotations() {
				ann[k] = v
			}
			if len(ordered) > 0 {
				membersJSON, _ := json.Marshal(ordered)
				ann[previewgroup.AnnotationGroupMembers] = string(membersJSON)
			}
			lbl := make(map[string]string, len(cur.GetLabels())+1)
			for k, v := range cur.GetLabels() {
				lbl[k] = v
			}
			lbl[previewgroup.LabelComposedGroup] = "true"

			updResp, uErr := pgClient.UpdatePreviewGroup(ctx, connect.NewRequest(&divergev1alpha1.UpdatePreviewGroupRequest{
				PreviewGroup: &divergev1alpha1.PreviewGroup{
					Name:            cur.Name,
					Namespace:       cur.Namespace,
					ResourceVersion: cur.ResourceVersion,
					Labels:          lbl,
					Annotations:     ann,
					Spec:            spec,
				},
			}))
			if uErr != nil {
				if connect.CodeOf(uErr) == connect.CodeAborted {
					continue
				}
				return errorToolResult(uErr), nil
			}
			finalPG = updResp.Msg.GetPreviewGroup()
			created = false
			break
		}

		if finalPG == nil {
			return errorToolResult(errors.New("conflict retries exhausted joining PreviewGroup")), nil
		}

		pgJSON, err := protojson.Marshal(finalPG)
		if err != nil {
			return errorToolResult(err), nil
		}
		payload, _ := json.Marshal(map[string]any{
			"created":       created,
			"group_name":    finalPG.Name,
			"members":       parseProtoMembers(finalPG.GetAnnotations()),
			"preview_group": json.RawMessage(pgJSON),
		})
		return &mcpruntime.CallToolResult{Content: json.RawMessage(payload)}, nil
	})

	registry.Register(mcpruntime.ToolDefinition{
		Name:        "diverge_leave_preview_group",
		Description: leavePreviewGroupToolDescription,
		InputSchema: leavePreviewGroupSchema,
	}, func(ctx context.Context, req mcpruntime.ToolRequest) (*mcpruntime.CallToolResult, error) {
		var params struct {
			GroupName        string   `json:"group_name"`
			Namespace        string   `json:"namespace"`
			ServiceNames     []string `json:"service_names"`
			EnvironmentNames []string `json:"environment_names"`
			Project          string   `json:"project"`
			MR               int      `json:"mr"`
		}
		if len(req.Arguments) > 0 {
			if err := json.Unmarshal(req.Arguments, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
		}
		if pgClient == nil {
			return errorToolResult(errors.New("preview group client is not configured")), nil
		}
		groupName, err := previewgroup.NormalizeGroupName(params.GroupName)
		if err != nil {
			return errorToolResult(err), nil
		}
		if len(params.ServiceNames) == 0 && len(params.EnvironmentNames) == 0 && params.MR <= 0 {
			return errorToolResult(errors.New("specify service_names, environment_names, or mr to detach from the PreviewGroup")), nil
		}

		ns := strings.TrimSpace(params.Namespace)
		if ns == "" {
			ns = "default"
		}

		getResp, getErr := pgClient.GetPreviewGroup(ctx, connect.NewRequest(&divergev1alpha1.GetPreviewGroupRequest{
			Name:      groupName,
			Namespace: ns,
		}))
		if getErr != nil {
			if connect.CodeOf(getErr) == connect.CodeNotFound {
				out, _ := json.Marshal(map[string]any{
					"group_name": groupName,
					"not_found":  true,
				})
				return &mcpruntime.CallToolResult{Content: json.RawMessage(out)}, nil
			}
			return errorToolResult(getErr), nil
		}

		cur := getResp.Msg.GetPreviewGroup()
		members := parseProtoMembers(cur.GetAnnotations())
		removeSet := make(map[string]bool)
		for _, s := range params.ServiceNames {
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				removeSet[trimmed] = true
			}
		}
		envSet := make(map[string]bool, len(params.EnvironmentNames))
		for _, e := range params.EnvironmentNames {
			trimmed := strings.TrimSpace(e)
			if slash := strings.Index(trimmed, "/"); slash >= 0 {
				trimmed = strings.TrimSpace(trimmed[slash+1:])
			}
			if trimmed != "" {
				envSet[trimmed] = true
			}
		}
		projClean := strings.TrimSpace(params.Project)
		for _, m := range members {
			if envSet[m.EnvironmentName] {
				removeSet[m.Service] = true
				continue
			}
			if params.MR > 0 && m.MR == params.MR && (projClean == "" || strings.EqualFold(m.Project, projClean)) {
				removeSet[m.Service] = true
			}
		}

		var keptSvcs []*divergev1alpha1.PreviewGroupServiceSpec
		var removed []string
		var remainingActive []string
		for _, svc := range cur.GetSpec().GetServices() {
			if removeSet[svc.GetName()] {
				removed = append(removed, svc.GetName())
				continue
			}
			keptSvcs = append(keptSvcs, svc)
			if svc.GetMode() != "baseline" {
				remainingActive = append(remainingActive, svc.GetName())
			}
		}

		if len(removed) == 0 {
			out, _ := json.Marshal(map[string]any{
				"group_name":         cur.Name,
				"removed_services":   []string{},
				"remaining_services": remainingActive,
				"deleted_group":      false,
			})
			return &mcpruntime.CallToolResult{Content: json.RawMessage(out)}, nil
		}

		if len(remainingActive) == 0 {
			delNS := cur.Namespace
			if delNS == "" {
				delNS = ns
			}
			if _, delErr := pgClient.DeletePreviewGroup(ctx, connect.NewRequest(&divergev1alpha1.DeletePreviewGroupRequest{
				Name:      cur.Name,
				Namespace: delNS,
			})); delErr != nil && connect.CodeOf(delErr) != connect.CodeNotFound {
				return errorToolResult(delErr), nil
			}
			out, _ := json.Marshal(map[string]any{
				"group_name":       cur.Name,
				"removed_services": removed,
				"deleted_group":    true,
			})
			return &mcpruntime.CallToolResult{Content: json.RawMessage(out)}, nil
		}

		var keptMembers []previewgroup.MemberRecord
		for _, m := range members {
			if !removeSet[m.Service] {
				keptMembers = append(keptMembers, m)
			}
		}
		ann := make(map[string]string, len(cur.GetAnnotations()))
		for k, v := range cur.GetAnnotations() {
			ann[k] = v
		}
		if len(keptMembers) > 0 {
			membersJSON, _ := json.Marshal(keptMembers)
			ann[previewgroup.AnnotationGroupMembers] = string(membersJSON)
		} else {
			delete(ann, previewgroup.AnnotationGroupMembers)
		}

		spec := cur.GetSpec()
		spec.Services = keptSvcs
		if _, uErr := pgClient.UpdatePreviewGroup(ctx, connect.NewRequest(&divergev1alpha1.UpdatePreviewGroupRequest{
			PreviewGroup: &divergev1alpha1.PreviewGroup{
				Name:            cur.Name,
				Namespace:       cur.Namespace,
				ResourceVersion: cur.ResourceVersion,
				Labels:          cur.GetLabels(),
				Annotations:     ann,
				Spec:            spec,
			},
		})); uErr != nil {
			return errorToolResult(uErr), nil
		}

		out, _ := json.Marshal(map[string]any{
			"group_name":         cur.Name,
			"removed_services":   removed,
			"remaining_services": remainingActive,
			"deleted_group":      false,
		})
		return &mcpruntime.CallToolResult{Content: json.RawMessage(out)}, nil
	})
}
