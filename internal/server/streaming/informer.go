package streaming

import (
	"context"
	"log/slog"
	"sync"
	"time"

	divergev1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// InformerManager manages shared informers and broadcasts events.
type InformerManager struct {
	EnvBroadcaster *Broadcaster[*divergev1alpha1.Environment]
	PgBroadcaster  *Broadcaster[*divergev1alpha1.PreviewGroup]
	logger         *slog.Logger

	snapMu sync.RWMutex
	envs   map[string]*divergev1alpha1.Environment
}

// NewInformerManager creates informer event handlers and connects them to broadcasters.
func NewInformerManager(logger *slog.Logger, metrics BroadcasterMetrics) *InformerManager {
	return &InformerManager{
		EnvBroadcaster: NewBroadcaster[*divergev1alpha1.Environment](metrics),
		PgBroadcaster:  NewBroadcaster[*divergev1alpha1.PreviewGroup](metrics),
		logger:         logger,
		envs:           make(map[string]*divergev1alpha1.Environment),
	}
}

// Close cleanly shuts down all broadcasters, unblocking active watch streams.
func (m *InformerManager) Close() {
	m.EnvBroadcaster.Close()
	m.PgBroadcaster.Close()
}

// SnapshotEnvironments returns a DeepCopy snapshot of the latest polled Environment CRs.
// Safe to call on a nil InformerManager (returns nil).
func (m *InformerManager) SnapshotEnvironments() []*divergev1alpha1.Environment {
	if m == nil {
		return nil
	}
	m.snapMu.RLock()
	defer m.snapMu.RUnlock()
	if len(m.envs) == 0 {
		return nil
	}
	out := make([]*divergev1alpha1.Environment, 0, len(m.envs))
	for _, env := range m.envs {
		if env != nil {
			out = append(out, env.DeepCopy())
		}
	}
	return out
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

	knownEnvs := make(map[string]*divergev1alpha1.Environment)
	knownPgs := make(map[string]*divergev1alpha1.PreviewGroup)

	poll := func() {
		pollCtx, pollCancel := context.WithTimeout(ctx, 10*time.Second)
		defer pollCancel()

		var envList divergev1alpha1.EnvironmentList
		if err := c.List(pollCtx, &envList); err != nil {
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
			m.snapMu.Lock()
			m.envs = currentEnvs
			m.snapMu.Unlock()
		}

		var pgList divergev1alpha1.PreviewGroupList
		if err := c.List(pollCtx, &pgList); err != nil {
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

	if ctx.Err() != nil {
		return
	}
	poll()
	if ctx.Err() != nil {
		return
	}

	go func() {
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
