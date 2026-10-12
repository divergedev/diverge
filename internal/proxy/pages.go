package proxy

import (
	"embed"
	"html/template"
	"net/http"
)

//go:embed templates/*.html
var templatesFS embed.FS

var tmpl = template.Must(template.ParseFS(templatesFS, "templates/*.html"))

// NotFoundData is the template data for the 404 page, including the requested
// environment name and a list of currently active environments.
type NotFoundData struct {
	EnvName       string
	ActiveEnvs    []EnvironmentInfo
	HideList      bool
	PreviewDomain string
}

func renderNotFound(w http.ResponseWriter, envName string, activeEnvs []EnvironmentInfo, hideList bool, previewDomain ...string) {
	w.WriteHeader(http.StatusNotFound)
	var domain string
	if len(previewDomain) > 0 {
		domain = previewDomain[0]
	}
	data := NotFoundData{
		EnvName:       envName,
		ActiveEnvs:    activeEnvs,
		HideList:      hideList,
		PreviewDomain: domain,
	}
	_ = tmpl.ExecuteTemplate(w, "404.html", data)
}

func renderLoading(w http.ResponseWriter, envInfo *EnvironmentInfo) {
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = tmpl.ExecuteTemplate(w, "loading.html", envInfo)
}

func renderError(w http.ResponseWriter, envInfo *EnvironmentInfo, errMsg string) {
	w.WriteHeader(http.StatusInternalServerError)
	data := struct {
		Env   *EnvironmentInfo
		Error string
	}{
		Env:   envInfo,
		Error: errMsg,
	}
	_ = tmpl.ExecuteTemplate(w, "error.html", data)
}
