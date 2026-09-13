{{/*
Expand the chart name.
*/}}

{{- define "tokenhub.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Fully qualified app name, used for resource names.
*/}}

{{- define "tokenhub.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name (include "tokenhub.name" .) | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{/*
Common labels.
*/}}

{{- define "tokenhub.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "tokenhub.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}

{{/*
Selector labels.
*/}}

{{- define "tokenhub.selectorLabels" -}}
app.kubernetes.io/name: {{ include "tokenhub.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Service account name.
*/}}

{{- define "tokenhub.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- include "tokenhub.fullname" . }}
{{- else }}
default
{{- end }}
{{- end }}

{{/*
Chart-managed credentials secret name.
*/}}

{{- define "tokenhub.secretName" -}}
{{- printf "%s-credentials" (include "tokenhub.fullname" .) }}
{{- end }}

{{/*
Database URL. Precedence: database.existingSecret (referenced directly, no
value to compose), then database.url, then the built-in subchart service.
Returns empty when the URL lives in an existing secret or is managed by the
External Secrets Operator.
*/}}

{{- define "tokenhub.databaseURL" -}}
{{- if .Values.database.existingSecret }}
{{- else if .Values.database.url }}
{{- .Values.database.url }}
{{- else if .Values.postgresql.enabled }}
postgresql://{{ .Values.postgresql.auth.username }}:{{ .Values.postgresql.auth.password }}@{{ .Release.Name }}-postgresql:5432/{{ .Values.postgresql.auth.database }}?sslmode=disable
{{- end }}
{{- end }}
