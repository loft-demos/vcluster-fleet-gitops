{{/*
Chart name + version for the helm.sh/chart label.
*/}}
{{- define "fleet-bindings.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Selector labels. This chart uses fixed resource names (not
{{ .Release.Name }}-prefixed) because it's a cluster singleton - one install
per vCluster Platform management cluster. Deployment.spec.selector is
immutable once created, so nothing may ever be added here without a manual
migration for existing installs.
*/}}
{{- define "fleet-bindings.selectorLabels" -}}
app.kubernetes.io/name: fleet-binding-controller
{{- end -}}

{{/*
Common labels: selector labels plus metadata-only labels that are always safe
to change on `helm upgrade`.
*/}}
{{- define "fleet-bindings.labels" -}}
{{ include "fleet-bindings.selectorLabels" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/part-of: fleet-bindings
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ include "fleet-bindings.chart" . }}
{{- end -}}
