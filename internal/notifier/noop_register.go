package notifier

import "github.com/divergedev/diverge/pkg/registry"

func init() {
	Providers.Register("noop", registry.Provider[Notifier]{
		Create: func(deps registry.Deps) (Notifier, error) {
			return &NoopNotifier{}, nil
		},
		Description: "No-op notification",
	})

	StatusProviders.Register("noop", registry.Provider[StatusReporter]{
		Create: func(deps registry.Deps) (StatusReporter, error) {
			return &NoopStatusReporter{}, nil
		},
		Description: "No-op status reporting",
	})

	GroupProviders.Register("noop", registry.Provider[PreviewGroupNotifier]{
		Create: func(deps registry.Deps) (PreviewGroupNotifier, error) {
			return &NoopPreviewGroupNotifier{}, nil
		},
		Description: "No-op group notifications",
	})
}
