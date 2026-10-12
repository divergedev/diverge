package streaming

import (
	"context"
	"log/slog"
	"time"

	divergev1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// InformerManager manages shared informers and broadcasts events.
type InformerManager struct {
	EnvBroadcaster *Broadcaster[*divergev1alpha1.Environment]
	PgBroadcaster  *Broadcaster[*divergev1alpha1.PreviewGroup]
	logger         *slog.Logger
}

// NewInformerManager creates informer event handlers and connects them to broadcasters.
func NewInformerManager(logger *slog.Logger, metrics BroadcasterMetrics) *InformerManager {
	return &InformerManager{
		EnvBroadcaster: NewBroadcaster[*divergev1alpha1.Environment](metrics),
		PgBroadcaster:  NewBroadcaster[*divergev1alpha1.PreviewGroup](metrics),
		logger:         logger,
	}
}

// Close cleanly shuts down all broadcasters, unblocking active watch streams.
func (m *InformerManager) Close() {
	m.EnvBroadcaster.Close()
	m.PgBroadcaster.Close()
}

// StartPolling periodically lists EnvironmentList and PreviewGroupList and diffs
// resource versions and UIDs to broadcast ADDED, MODIFIED, and DELETED events.
func (m *InformerManager) StartPolling(ctx context.Context, c client.Client, interval time.Duration) {
	if c == nil {
		return
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}

	go func() {
		knownEnvs := make(map[string]*divergev1alpha1.Environment)
		knownPgs := make(map[string]*divergev1alpha1.PreviewGroup)

		poll := func() {
			var envList divergev1alpha1.EnvironmentList
			if err := c.List(ctx, &envList); err != nil {
				if ctx.Err() == nil && m.logger != nil {
					m.logger.Warn("informer poll failed to list environments", "error", err)
				}
			} else {
				currentEnvs := make(map[string]*divergev1alpha1.Environment, len(envList.Items))
				for i := range envList.Items {
					item := envList.Items[i].DeepCopy()
					key := item.Namespace + "/" + item.Name
					currentEnvs[key] = item
					if prev, exists := knownEnvs[key]; !exists {
						m.HandleEnvironmentEvent("ADDED", item)
					} else if prev.UID != item.UID {
						m.HandleEnvironmentEvent("DELETED", prev)
						m.HandleEnvironmentEvent("ADDED", item)
					} else if prev.ResourceVersion != item.ResourceVersion {
						m.HandleEnvironmentEvent("MODIFIED", item)
					}
				}
				for key, prev := range knownEnvs {
					if _, exists := currentEnvs[key]; !exists {
						m.HandleEnvironmentEvent("DELETED", prev)
					}
				}
				knownEnvs = currentEnvs
			}

			var pgList divergev1alpha1.PreviewGroupList
			if err := c.List(ctx, &pgList); err != nil {
				if ctx.Err() == nil && m.logger != nil {
					m.logger.Warn("informer poll failed to list previewgroups", "error", err)
				}
			} else {
				currentPgs := make(map[string]*divergev1alpha1.PreviewGroup, len(pgList.Items))
				for i := range pgList.Items {
					item := pgList.Items[i].DeepCopy()
					key := item.Namespace + "/" + item.Name
					currentPgs[key] = item
					if prev, exists := knownPgs[key]; !exists {
						m.HandlePreviewGroupEvent("ADDED", item)
					} else if prev.UID != item.UID {
						m.HandlePreviewGroupEvent("DELETED", prev)
						m.HandlePreviewGroupEvent("ADDED", item)
					} else if prev.ResourceVersion != item.ResourceVersion {
						m.HandlePreviewGroupEvent("MODIFIED", item)
					}
				}
				for key, prev := range knownPgs {
					if _, exists := currentPgs[key]; !exists {
						m.HandlePreviewGroupEvent("DELETED", prev)
					}
				}
				knownPgs = currentPgs
			}
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if m.EnvBroadcaster.IsClosed() && m.PgBroadcaster.IsClosed() {
					return
				}
				poll()
			}
		}
	}()
}

// HandleEnvironmentEvent handles an environment event from an informer.
func (m *InformerManager) HandleEnvironmentEvent(eventType string, obj client.Object) {
	env, ok := obj.(*divergev1alpha1.Environment)
	if !ok {
		return
	}
	m.EnvBroadcaster.Publish(Event[*divergev1alpha1.Environment]{
		Type:    eventType,
		Object:  env,
		Version: env.ResourceVersion,
	})
}

// HandlePreviewGroupEvent handles a preview group event from an informer.
func (m *InformerManager) HandlePreviewGroupEvent(eventType string, obj client.Object) {
	pg, ok := obj.(*divergev1alpha1.PreviewGroup)
	if !ok {
		return
	}
	m.PgBroadcaster.Publish(Event[*divergev1alpha1.PreviewGroup]{
		Type:    eventType,
		Object:  pg,
		Version: pg.ResourceVersion,
	})
}
