package argocd

type ServiceConfig struct {
	Name       string
	ChartPath  string
	ValuesFile string
	Image      string
	Tag        string
}

type ApplicationStatus struct {
	Name       string
	SyncStatus string // Synced, OutOfSync, Unknown
	Health     string // Healthy, Progressing, Degraded, Missing
}
