{{/* Common labels applied to every resource. */}}
{{- define "ads.labels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- end }}

{{/* Selector labels — must NOT include version (selectors are immutable). */}}
{{- define "ads.selectorLabels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/* Fully-qualified release name. */}}
{{- define "ads.fullname" -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* DB host — bundled mode resolves to the Bitnami subchart's primary
     service (<release>-postgresql); external mode uses .Values.database.host. */}}
{{- define "ads.dbHost" -}}
{{- if .Values.postgresql.enabled -}}
{{- printf "%s-postgresql" .Release.Name -}}
{{- else -}}
{{- required "database.host is required when postgresql.enabled = false" .Values.database.host -}}
{{- end -}}
{{- end }}

{{/* DB password Secret name — bundled mode points at the Bitnami-generated
     <release>-postgresql secret (key: "password"); external mode uses the
     operator-supplied passwordSecret OR the ads-secrets Secret rendered
     by templates/secret.yaml. */}}
{{- define "ads.dbPasswordSecret" -}}
{{- if .Values.postgresql.enabled -}}
{{- if .Values.postgresql.auth.existingSecret -}}
{{- .Values.postgresql.auth.existingSecret -}}
{{- else -}}
{{- printf "%s-postgresql" .Release.Name -}}
{{- end -}}
{{- else -}}
{{- if .Values.database.passwordSecret -}}
{{- .Values.database.passwordSecret -}}
{{- else -}}
{{- printf "%s-secrets" (include "ads.fullname" .) -}}
{{- end -}}
{{- end -}}
{{- end }}

{{/* DB password Secret key — Bitnami uses "password"; the ads-managed
     fallback Secret uses "db_password". */}}
{{- define "ads.dbPasswordKey" -}}
{{- if .Values.postgresql.enabled -}}
password
{{- else if .Values.database.passwordSecret -}}
password
{{- else -}}
db_password
{{- end -}}
{{- end }}

{{/* TLS Secret name — operator-supplied existingSecret takes priority,
     else the chart-managed <release>-ads-tls Secret rendered by tls.yaml. */}}
{{- define "ads.tlsSecret" -}}
{{- if .Values.tls.existingSecret -}}
{{- .Values.tls.existingSecret -}}
{{- else -}}
{{- printf "%s-ads-tls" .Release.Name -}}
{{- end -}}
{{- end }}
