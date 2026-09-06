{{- define "diverge.fullname" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "diverge.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "diverge.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "diverge.selectorLabels" -}}
app.kubernetes.io/name: {{ include "diverge.fullname" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Controller image
*/}}
{{- define "diverge.image" -}}
{{- printf "%s:%s" .Values.image.repository (.Values.image.tag | default .Chart.AppVersion) -}}
{{- end }}

{{/*
Server image
*/}}
{{- define "diverge.server.image" -}}
{{- $repo := .Values.image.repository -}}
{{- if and .Values.server .Values.server.image .Values.server.image.repository -}}
  {{- $repo = .Values.server.image.repository -}}
{{- end -}}
{{- $tag := .Values.image.tag | default .Chart.AppVersion -}}
{{- if and .Values.server .Values.server.image .Values.server.image.tag -}}
  {{- $tag = .Values.server.image.tag -}}
{{- end -}}
{{- printf "%s:%s" $repo $tag -}}
{{- end }}

{{/*
Proxy image
*/}}
{{- define "diverge.proxy.image" -}}
{{- $repo := .Values.image.repository -}}
{{- if and .Values.proxy .Values.proxy.image .Values.proxy.image.repository -}}
  {{- $repo = .Values.proxy.image.repository -}}
{{- end -}}
{{- $tag := .Values.image.tag | default .Chart.AppVersion -}}
{{- if and .Values.proxy .Values.proxy.image .Values.proxy.image.tag -}}
  {{- $tag = .Values.proxy.image.tag -}}
{{- end -}}
{{- printf "%s:%s" $repo $tag -}}
{{- end }}

{{/*
Activator Proxy image
*/}}
{{- define "diverge.activatorProxy.image" -}}
{{- $repo := "ghcr.io/divergedev/activator-proxy" -}}
{{- if and .Values.activatorProxy .Values.activatorProxy.image .Values.activatorProxy.image.repository -}}
  {{- $repo = .Values.activatorProxy.image.repository -}}
{{- end -}}
{{- $tag := .Chart.AppVersion -}}
{{- if and .Values.activatorProxy .Values.activatorProxy.image .Values.activatorProxy.image.tag -}}
  {{- $tag = .Values.activatorProxy.image.tag -}}
{{- end -}}
{{- printf "%s:%s" $repo $tag -}}
{{- end }}

