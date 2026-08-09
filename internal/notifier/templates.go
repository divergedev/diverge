package notifier

import (
	"bytes"
	"text/template"
)

var (
	createdTemplate  = template.Must(template.New("created").Parse(createdTmplStr))
	readyTemplate    = template.Must(template.New("ready").Parse(readyTmplStr))
	failedTemplate   = template.Must(template.New("failed").Parse(failedTmplStr))
	teardownTemplate = template.Must(template.New("teardown").Parse(teardownTmplStr))
)

const createdTmplStr = `## 🚀 Diverge Preview Environment

| Field | Value |
|-------|-------|
| **Status** | ⏳ Deploying... |
| **Environment** | ` + "`{{.Name}}`" + ` |
| **Branch** | ` + "`{{.Branch}}`" + ` |
| **Deploy Mode** | {{.Mode}} |
| **Routing** | {{.RoutingMode}} |

### Services Being Deployed
{{range .Services}}- ⏳ {{.}}
{{end}}
---
_Powered by [Diverge](https://github.com/divergedev/diverge) • Environment will auto-expire in {{.TTL}}_`

const readyTmplStr = `## 🟢 Diverge Preview Environment — Ready!

| Field | Value |
|-------|-------|
| **Status** | ✅ Running |
| **URL** | [🔗 Open Preview]({{.URL}}) |
| **Environment** | ` + "`{{.Name}}`" + ` |
| **Branch** | ` + "`{{.Branch}}`" + ` |
| **Deploy Mode** | {{.Mode}} ({{.NumServices}} services deployed) |
| **Deployed In** | {{.Duration}} |

### Services
{{range .Services}}- ✅ {{.}}
{{end}}
### Quick Access
` + "```\n" + `curl -H "x-diverge-env: {{.Name}}" {{.BaseURL}}
` + "```\n" + `
> 💡 **Tip:** Use the [Diverge browser extension](#) or add the header ` + "`x-diverge-env: {{.Name}}`" + ` to route traffic to this preview.

---
_Powered by [Diverge](https://github.com/divergedev/diverge) • Expires {{.ExpiryTime}}_`

const failedTmplStr = `## 🔴 Diverge Preview Environment — Failed

| Field | Value |
|-------|-------|
| **Status** | ❌ Failed |
| **Environment** | ` + "`{{.Name}}`" + ` |
| **Reason** | {{.Reason}} |

### Conditions
{{range .Conditions}}- {{.Icon}} {{.Type}}: {{.Message}}
{{end}}
Check the controller logs for details:
` + "```\n" + `kubectl logs -l app=diverge-controller -n diverge-system
` + "```\n"

const teardownTmplStr = `## 🗑️ Diverge Preview Environment — Destroyed

Environment ` + "`{{.Name}}`" + ` has been cleaned up.
**Reason:** {{.Reason}}`

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
