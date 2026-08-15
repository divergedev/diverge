package notifier

import (
	"text/template"
)

var (
	pgCreatedTemplate  = template.Must(template.New("pg_created").Funcs(funcMap).Parse(pgCreatedTmplStr))
	pgReadyTemplate    = template.Must(template.New("pg_ready").Funcs(funcMap).Parse(pgReadyTmplStr))
	pgFailedTemplate   = template.Must(template.New("pg_failed").Funcs(funcMap).Parse(pgFailedTmplStr))
	pgTeardownTemplate = template.Must(template.New("pg_teardown").Funcs(funcMap).Parse(pgTeardownTmplStr))
)

const pgCreatedTmplStr = `## 🔀 Diverge Preview — Deploying

| Field | Value |
|-------|-------|
| **Environment** | ` + "`{{.Name | sanitize}}`" + ` |
| **Branch** | ` + "`{{.Branch | sanitize}}`" + ` |
| **MR** | !{{.MR}} |

### Services
| Service | Mode | Image |
|---------|------|-------|
{{range .Services}}| {{.Name | sanitize}} | {{.ModeEmoji}} {{.Mode | sanitize}} | {{.ImageOrBaseline | sanitize}} |
{{end}}
---
_Powered by [Diverge](https://github.com/divergedev/diverge) • Expires in {{.TTL}}` + "_"

const pgReadyTmplStr = `## 🟢 Diverge Preview — Ready!

| Field | Value |
|-------|-------|
| **Status** | ✅ Running |
{{if .URL}}| **Preview Link** | [🔗 Open Preview]({{.URL | sanitize}}?preview={{.HeaderValue | sanitize}}) |
{{end}}| **Environment** | ` + "`{{.Name | sanitize}}`" + ` |
| **Services** | {{.ServiceCount}} ({{.RunningCount}} running) |

### Services
| Service | Status | Namespace |
|---------|--------|-----------|
{{range .Services}}| {{.Emoji}} {{.Name | sanitize}} | {{.Phase | sanitize}} | {{.Namespace | sanitize}} |
{{end}}
### Quick Access
` + "```\n" + `curl -H "{{.HeaderKey | sanitize}}: {{.HeaderValue | sanitize}}" {{.BaseURL | sanitize}}
` + "```\n" + `
> 💡 **Tip:** Click the preview link or add header ` + "`{{.HeaderKey | sanitize}}: {{.HeaderValue | sanitize}}`" + ` to route traffic.

---
_Powered by [Diverge](https://github.com/divergedev/diverge) • Expires {{.ExpiryTime}}` + "_"

const pgFailedTmplStr = `## 🔴 Diverge Preview — Failed

| Field | Value |
|-------|-------|
| **Status** | ❌ Failed |
| **Environment** | ` + "`{{.Name | sanitize}}`" + ` |
| **Reason** | {{.Reason | sanitize}} |

### Services
| Service | Status | Reason |
|---------|--------|--------|
{{range .Services}}| {{.Emoji}} {{.Name | sanitize}} | {{.Phase | sanitize}} | {{.Reason | sanitize}} |
{{end}}`

const pgTeardownTmplStr = `## 🗑️ Diverge Preview — Destroyed

Environment ` + "`{{.Name | sanitize}}`" + ` has been cleaned up.
**Reason:** {{.Reason | sanitize}}`

// PreviewGroupTemplateData represents the configuration or state for this type.
type PreviewGroupTemplateData struct {
	Name         string
	Branch       string
	MR           int
	TTL          string
	URL          string
	BaseURL      string
	HeaderKey    string
	HeaderValue  string
	ServiceCount int
	RunningCount int
	ExpiryTime   string
	Reason       string
	Services     []PreviewGroupServiceTemplateData
}

// PreviewGroupServiceTemplateData represents the configuration or state for this type.
type PreviewGroupServiceTemplateData struct {
	Name            string
	Mode            string
	ModeEmoji       string
	ImageOrBaseline string
	Emoji           string
	Phase           string
	Namespace       string
	Reason          string
}
