package notifier

import (
	"bytes"
	"strings"
	"text/template"
)

func sanitizeMarkdown(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "`", "\\`")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "@", "@\u200b") // zero-width space prevents mentions
	return s
}

var funcMap = template.FuncMap{"sanitize": sanitizeMarkdown}

var (
	createdTemplate  = template.Must(template.New("created").Funcs(funcMap).Parse(createdTmplStr))
	readyTemplate    = template.Must(template.New("ready").Funcs(funcMap).Parse(readyTmplStr))
	failedTemplate   = template.Must(template.New("failed").Funcs(funcMap).Parse(failedTmplStr))
	teardownTemplate = template.Must(template.New("teardown").Funcs(funcMap).Parse(teardownTmplStr))
)

const createdTmplStr = `## 🚀 Diverge Preview Environment

| Field | Value |
|-------|-------|
| **Status** | ⏳ Deploying... |
| **Environment** | ` + "`{{.Name | sanitize}}`" + ` |
| **Branch** | ` + "`{{.Branch | sanitize}}`" + ` |
| **Deploy Mode** | {{.Mode | sanitize}} |
| **Routing** | {{.RoutingMode | sanitize}} |

### Services Being Deployed
{{range .Services}}- ⏳ {{. | sanitize}}
{{end}}
---
_Powered by [Diverge](https://github.com/divergedev/diverge) • Environment will auto-expire in {{.TTL}}_`

const readyTmplStr = `## 🟢 Diverge Preview Environment — Ready!

| Field | Value |
|-------|-------|
| **Status** | ✅ Running |
| **URL** | [🔗 Open Preview]({{.URL}}) |
| **Environment** | ` + "`{{.Name | sanitize}}`" + ` |
| **Branch** | ` + "`{{.Branch | sanitize}}`" + ` |
| **Deploy Mode** | {{.Mode | sanitize}} ({{.NumServices}} services deployed) |
| **Deployed In** | {{.Duration}} |

### Services
{{range .Services}}- ✅ {{. | sanitize}}
{{end}}
{{if .BaseURL}}### Quick Access
` + "```\n" + `curl -H "x-diverge-env: {{.Name | sanitize}}" {{.BaseURL}}
` + "```\n" + `
> 💡 **Tip:** Use the Diverge browser extension or add the header ` + "`x-diverge-env: {{.Name | sanitize}}`" + ` to route traffic to this preview.
{{end}}
---
_Powered by [Diverge](https://github.com/divergedev/diverge) • Expires {{.ExpiryTime}}_`

const failedTmplStr = `## 🔴 Diverge Preview Environment — Failed

| Field | Value |
|-------|-------|
| **Status** | ❌ Failed |
| **Environment** | ` + "`{{.Name | sanitize}}`" + ` |
| **Reason** | {{.Reason | sanitize}} |

### Conditions
{{range .Conditions}}- {{.Icon}} {{.Type | sanitize}}: {{.Message | sanitize}}
{{end}}
Check the controller logs for details:
` + "```\n" + `kubectl logs -l app=diverge-controller -n diverge-system
` + "```\n"

const teardownTmplStr = `## 🗑️ Diverge Preview Environment — Destroyed

Environment ` + "`{{.Name | sanitize}}`" + ` has been cleaned up.
**Reason:** {{.Reason | sanitize}}`

type TemplateData struct {
	Name        string
	Branch      string
	Mode        string
	RoutingMode string
	Services    []string
	TTL         string
	URL         string
	NumServices int
	Duration    string
	BaseURL     string
	ExpiryTime  string
	Reason      string
	Conditions  []ConditionData
}

type ConditionData struct {
	Icon    string
	Type    string
	Message string
}

func renderTemplate(tmpl *template.Template, data TemplateData) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
