{{- define "equipment-emulator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "equipment-emulator.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{- define "equipment-emulator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "equipment-emulator.labels" -}}
helm.sh/chart: {{ include "equipment-emulator.chart" . }}
{{ include "equipment-emulator.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels. Deployment and Service also pin
app.kubernetes.io/component (emulator): a Service selects exactly one
Deployment (tests/test_service_selectors.py).
*/}}
{{- define "equipment-emulator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "equipment-emulator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "equipment-emulator.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "equipment-emulator.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "equipment-emulator.image" -}}
{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}
{{- end }}

{{/*
The chart renders the observability env itself (ADR 0005); extraEnv must not
shadow it.
*/}}
{{- define "equipment-emulator.requireNoOwnedEnv" -}}
{{- $owned := list "ENVIRONMENT" "SERVICE_VERSION" "OTEL_SERVICE_NAME" "OTEL_EXPORTER_OTLP_ENDPOINT" -}}
{{- range .Values.extraEnv -}}
{{- if has (toString .name) $owned -}}
{{- fail (printf "extraEnv sets %s: the chart renders it itself (otel.*, serviceVersion, environment); set the value there, never twice." .name) -}}
{{- end -}}
{{- end -}}
{{- end -}}
