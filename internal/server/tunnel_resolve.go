package server

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/multicluster"
	"github.com/divergedev/diverge/internal/server/auth"
)

// TunnelRecord describes a registered developer tunnel returned by TunnelDirectory.
type TunnelRecord struct {
	TunnelID         string
	Namespace        string
	PreviewID        string
	Service          string
	Port             int32
	ProtocolVersion  int32
	LeaseName        string
	K8sServiceName   string
	Owner            string
	ClientInstanceID string
	HolderPod        string
	Metadata         map[string]string
	StartedAt        time.Time
	ExpiresAt        time.Time
}

// TunnelFilter constrains TunnelDirectory.List queries (empty fields match all).
type TunnelFilter struct {
	Namespace string
	PreviewID string
	Service   string
}

// TunnelDirectory abstracts active tunnel lookup for routing, ResolvePreview,
// and ListTunnels. In v0.30.0 it is backed by local in-memory TunnelManager state.
type TunnelDirectory interface {
	Lookup(namespace, previewID, service string) (*TunnelRecord, bool)
	List(filter TunnelFilter) []TunnelRecord
}

type localTunnelDirectory struct {
	tm *TunnelManager
}

func cloneStringMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func tunnelRecordFromActive(at *activeTunnel, podName string) TunnelRecord {
	return TunnelRecord{
		TunnelID:         at.tunnelID,
		Namespace:        at.namespace,
		PreviewID:        at.previewID,
		Service:          at.service,
		Port:             at.port,
		ProtocolVersion:  at.protocolVersion,
		LeaseName:        at.leaseName,
		K8sServiceName:   at.k8sSvcName,
		Owner:            at.owner,
		ClientInstanceID: at.clientInstanceID,
		HolderPod:        podName,
		Metadata:         cloneStringMap(at.metadata),
		StartedAt:        at.startedAt,
		ExpiresAt:        at.expiresAt,
	}
}

func (d *localTunnelDirectory) Lookup(namespace, previewID, service string) (*TunnelRecord, bool) {
	if d == nil || d.tm == nil {
		return nil, false
	}
	d.tm.mu.RLock()
	defer d.tm.mu.RUnlock()
	at, ok := d.tm.tunnels[tunnelMapKey(namespace, previewID, service)]
	if !ok || at == nil {
		return nil, false
	}
	rec := tunnelRecordFromActive(at, d.tm.podName)
	return &rec, true
}

func (d *localTunnelDirectory) List(filter TunnelFilter) []TunnelRecord {
	if d == nil || d.tm == nil {
		return nil
	}
	d.tm.mu.RLock()
	defer d.tm.mu.RUnlock()
	var out []TunnelRecord
	for _, at := range d.tm.tunnels {
		if at == nil {
			continue
		}
		if filter.Namespace != "" && at.namespace != filter.Namespace {
			continue
		}
		if filter.PreviewID != "" && at.previewID != filter.PreviewID {
			continue
		}
		if filter.Service != "" && at.service != filter.Service {
			continue
		}
		out = append(out, tunnelRecordFromActive(at, d.tm.podName))
	}
	return out
}

func (tm *TunnelManager) dir() TunnelDirectory {
	if tm == nil {
		return &localTunnelDirectory{}
	}
	if tm.directory != nil {
		return tm.directory
	}
	return &localTunnelDirectory{tm: tm}
}

type resolveTargetKey struct {
	namespace string
	service   string
}

func normalizeProxyBaseURL(addr string) string {
	trimmed := strings.TrimSpace(addr)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		return trimmed
	}
	return "http://" + trimmed
}

// ResolvePreview returns active preview targets combining running hub-cluster
// Environment CRs and active developer tunnels (tunnels take precedence per service).
func (tm *TunnelManager) ResolvePreview(_ context.Context, req *connect.Request[pb.ResolvePreviewRequest]) (*connect.Response[pb.ResolvePreviewResponse], error) {
	previewID := strings.TrimSpace(req.Msg.GetPreviewId())
	if err := ValidateDNS1123Label(previewID, "preview_id"); err != nil {
		recordResolveRequest("invalid_argument")
		return nil, err
	}

	namespace := strings.TrimSpace(req.Msg.GetNamespace())
	if namespace != "" {
		if err := ValidateDNS1123Label(namespace, "namespace"); err != nil {
			recordResolveRequest("invalid_argument")
			return nil, err
		}
		if !tm.isNamespaceAllowed(namespace) {
			recordResolveRequest("permission_denied")
			return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("namespace %q is not allowed", namespace))
		}
	} else if tm.defaultNamespace != "" {
		namespace = tm.defaultNamespace
		if !tm.isNamespaceAllowed(namespace) {
			recordResolveRequest("permission_denied")
			return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("namespace %q is not allowed", namespace))
		}
	}

	merged := make(map[resolveTargetKey]*pb.ResolvedTarget)

	// Pass 1: In-cluster running Environment CRs matched by HeaderValue or Name.
	if tm.informerMgr != nil {
		envs := tm.informerMgr.SnapshotEnvironments()
		var matchedEnvs []*divergeiov1alpha1.Environment
		for _, env := range envs {
			if env == nil || env.DeletionTimestamp != nil || env.Status.Phase != divergeiov1alpha1.PhaseRunning {
				continue
			}
			if namespace != "" {
				if env.Namespace != namespace {
					continue
				}
			} else if !tm.isNamespaceAllowed(env.Namespace) {
				continue
			}
			if hdr := strings.TrimSpace(env.Spec.Routing.HeaderValue); hdr != "" {
				if hdr != previewID {
					continue
				}
			} else if env.Name != previewID {
				continue
			}
			if multicluster.IsRemoteEnv(env) || multicluster.HasConfirmedRemoteTarget(env) {
				continue
			}
			matchedEnvs = append(matchedEnvs, env)
		}

		slices.SortFunc(matchedEnvs, func(a, b *divergeiov1alpha1.Environment) int {
			return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
		})

		for _, env := range matchedEnvs {
			owner := env.Annotations["divergedev.com/created-by"]
			targetReady := !strings.EqualFold(strings.TrimSpace(env.Annotations["divergedev.com/hibernated"]), "true")
			if cfg := env.Spec.ServiceConfig; cfg != nil {
				if svcName := strings.TrimSpace(cfg.ServiceName); svcName != "" {
					target := &pb.ResolvedTarget{
						Service:         svcName,
						Namespace:       env.Namespace,
						Mode:            pb.ResolvedTargetMode_RESOLVED_TARGET_MODE_ENVIRONMENT,
						Ready:           targetReady,
						EnvironmentName: env.Name,
						Owner:           owner,
					}
					if cfg.Port > 0 && strings.TrimSpace(cfg.Endpoint) == "" {
						deployNS := env.Namespace
						if env.Spec.Deploy.Namespace == "create" {
							deployNS = env.PreviewNamespace()
						}
						k8sSvcName := divergeiov1alpha1.ServiceConfigServiceName(env.Name, svcName)
						target.Port = cfg.Port
						target.UpstreamUrl = fmt.Sprintf(
							"http://%s.%s.svc.cluster.local:%d",
							k8sSvcName,
							deployNS,
							cfg.Port,
						)
					}
					merged[resolveTargetKey{namespace: env.Namespace, service: svcName}] = target
				}
			}

			for _, rawSvc := range env.Spec.Deploy.ChangedServices {
				svc := strings.TrimSpace(rawSvc)
				if svc == "" {
					continue
				}
				key := resolveTargetKey{namespace: env.Namespace, service: svc}
				if _, exists := merged[key]; !exists {
					merged[key] = &pb.ResolvedTarget{
						Service:         svc,
						Namespace:       env.Namespace,
						Mode:            pb.ResolvedTargetMode_RESOLVED_TARGET_MODE_ENVIRONMENT,
						Ready:           targetReady,
						EnvironmentName: env.Name,
						Owner:           owner,
					}
				}
			}
		}
	}

	// Pass 2: Active local developer tunnels (tunnel wins on same namespace+service).
	proxyBaseURL := normalizeProxyBaseURL(tm.proxyServiceAddr)
	for _, rec := range tm.dir().List(TunnelFilter{Namespace: namespace, PreviewID: previewID}) {
		if !tm.isNamespaceAllowed(rec.Namespace) || (namespace != "" && rec.Namespace != namespace) || rec.PreviewID != previewID {
			continue
		}
		proxyHeaders := map[string]string{
			"X-Diverge-Env":       rec.PreviewID,
			"X-Diverge-Namespace": rec.Namespace,
		}
		if rec.Service != "" {
			proxyHeaders["X-Diverge-Service"] = rec.Service
		}
		merged[resolveTargetKey{namespace: rec.Namespace, service: rec.Service}] = &pb.ResolvedTarget{
			Service:      rec.Service,
			Namespace:    rec.Namespace,
			Mode:         pb.ResolvedTargetMode_RESOLVED_TARGET_MODE_TUNNEL,
			Ready:        true,
			UpstreamUrl:  proxyBaseURL,
			ProxyHeaders: proxyHeaders,
			TunnelId:     rec.TunnelID,
			Port:         rec.Port,
			Owner:        rec.Owner,
			Metadata:     cloneStringMap(rec.Metadata),
		}
	}

	targets := make([]*pb.ResolvedTarget, 0, len(merged))
	for _, target := range merged {
		targets = append(targets, target)
	}
	slices.SortFunc(targets, func(a, b *pb.ResolvedTarget) int {
		return cmp.Or(cmp.Compare(a.Service, b.Service), cmp.Compare(a.Namespace, b.Namespace))
	})

	if len(targets) > 0 {
		recordResolveRequest("hit")
	} else {
		recordResolveRequest("miss")
	}

	return connect.NewResponse(&pb.ResolvePreviewResponse{
		PreviewId: previewID,
		Namespace: namespace,
		Targets:   targets,
	}), nil
}

// ListTunnels returns active tunnels visible to the authenticated caller under K8s RBAC SAR.
func (tm *TunnelManager) ListTunnels(ctx context.Context, req *connect.Request[pb.ListTunnelsRequest]) (*connect.Response[pb.ListTunnelsResponse], error) {
	user, ok := auth.UserInfoFromContext(ctx)
	if !ok || user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("not authenticated"))
	}
	if !user.ExpiresAt.IsZero() && !user.ExpiresAt.After(time.Now()) {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication token has expired"))
	}

	namespace := strings.TrimSpace(req.Msg.GetNamespace())
	previewID := strings.TrimSpace(req.Msg.GetPreviewId())
	service := strings.TrimSpace(req.Msg.GetService())

	if namespace != "" {
		if err := ValidateDNS1123Label(namespace, "namespace"); err != nil {
			return nil, err
		}
	}
	if previewID != "" {
		if err := ValidateDNS1123Label(previewID, "preview_id"); err != nil {
			return nil, err
		}
	}
	if service != "" {
		if err := ValidateDNS1123Label(service, "service"); err != nil {
			return nil, err
		}
	}

	if tm.k8sClient == nil && !isMacaroonUser(user) {
		return nil, connect.NewError(connect.CodeInternal, errors.New("authorization backend unavailable"))
	}

	if namespace != "" {
		if !tm.isNamespaceAllowed(namespace) {
			return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("namespace %q is not allowed", namespace))
		}
		if err := AuthorizeAction(ctx, tm.k8sClient, tm.auditLogger, "list", namespace, "environments"); err != nil {
			return nil, err
		}
	}

	records := tm.dir().List(TunnelFilter{
		Namespace: namespace,
		PreviewID: previewID,
		Service:   service,
	})

	sarCache := make(map[string]bool)
	kept := make([]TunnelRecord, 0, len(records))
	for _, rec := range records {
		if !tm.isNamespaceAllowed(rec.Namespace) || (namespace != "" && rec.Namespace != namespace) {
			continue
		}
		if namespace == "" {
			allowed, checked := sarCache[rec.Namespace]
			if !checked {
				err := AuthorizeAction(ctx, tm.k8sClient, tm.auditLogger, "list", rec.Namespace, "environments")
				switch {
				case err == nil:
					allowed = true
				case connect.CodeOf(err) == connect.CodePermissionDenied:
					allowed = false
				default:
					return nil, err
				}
				sarCache[rec.Namespace] = allowed
			}
			if !allowed {
				continue
			}
		}
		kept = append(kept, rec)
	}

	slices.SortFunc(kept, func(a, b TunnelRecord) int {
		return cmp.Or(
			cmp.Compare(a.Namespace, b.Namespace),
			cmp.Compare(a.PreviewID, b.PreviewID),
			cmp.Compare(a.Service, b.Service),
			cmp.Compare(a.TunnelID, b.TunnelID),
		)
	})

	out := make([]*pb.TunnelInfo, 0, len(kept))
	for _, rec := range kept {
		info := &pb.TunnelInfo{
			TunnelId:  rec.TunnelID,
			Namespace: rec.Namespace,
			PreviewId: rec.PreviewID,
			Service:   rec.Service,
			Port:      rec.Port,
			Owner:     rec.Owner,
			HolderPod: rec.HolderPod,
			Metadata:  cloneStringMap(rec.Metadata),
		}
		if !rec.StartedAt.IsZero() {
			info.StartedAt = timestamppb.New(rec.StartedAt)
		}
		if !rec.ExpiresAt.IsZero() {
			info.ExpiresAt = timestamppb.New(rec.ExpiresAt)
		}
		out = append(out, info)
	}

	return connect.NewResponse(&pb.ListTunnelsResponse{
		Tunnels: out,
	}), nil
}

// divergev1alpha1connect interface compliance
var _ divergev1alpha1connect.PreviewResolverServiceHandler = (*TunnelManager)(nil)
