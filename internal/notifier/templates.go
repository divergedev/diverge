package notifier

import (
	"bytes"
	"regexp"
	"strings"
	"text/template"
)

// validHeaderKey matches RFC 7230 token characters (alphanumeric + -)
var validHeaderKey = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)

// sanitizeHeaderKey validates and returns a safe header key name.
func sanitizeHeaderKey(key string) string {
	if !validHeaderKey.MatchString(key) {
		return "x-diverge-env"
	}
	return key
}

func sanitizeMarkdown(s string) string {
	s = strings.ReplaceAll(s, "\\|", "|")
	s = strings.ReplaceAll(s, "\\`", "`")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "@\u200b", "@")

	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "`", "\\`")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "@", "@\u200b")
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
` + "```\n" + `curl -H "{{.HeaderKey | sanitize}}: {{.Name | sanitize}}" {{.BaseURL}}
` + "```\n" + `
> 💡 **Tip:** Use the Diverge browser extension or add the header ` + "`{{.HeaderKey | sanitize}}: {{.Name | sanitize}}`" + ` to route traffic to this preview.
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

// TemplateData holds the values passed to notification message templates.
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
	HeaderKey   string // routing header key (defaults to x-diverge-env)
	Conditions  []ConditionData
}

// ConditionData represents a single status condition for template rendering.
type ConditionData struct {
	Icon    string
	Type    string
	Message string
}

func renderTemplate(tmpl *template.Template, data any) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
