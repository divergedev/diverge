package streaming

import (
	"log/slog"

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
func NewInformerManager(logger *slog.Logger, metrics ...BroadcasterMetrics) *InformerManager {
	return &InformerManager{
		EnvBroadcaster: NewBroadcaster[*divergev1alpha1.Environment](metrics...),
		PgBroadcaster:  NewBroadcaster[*divergev1alpha1.PreviewGroup](metrics...),
		logger:         logger,
	}
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
