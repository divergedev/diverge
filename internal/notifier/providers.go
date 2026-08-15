package notifier

import "github.com/divergedev/diverge/pkg/registry"

var (
	Providers       = registry.New[Notifier]("notifier")
	StatusProviders = registry.New[StatusReporter]("status-reporter")
	GroupProviders  = registry.New[PreviewGroupNotifier]("previewgroup-notifier")
)
