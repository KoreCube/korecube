{{- define "korecube.name" -}}
{{- default .Chart.Name .Values.app.name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "korecube.fullname" -}}
{{- printf "%s" (include "korecube.name" .) -}}
{{- end -}}

{{- define "korecube.labels" -}}
app.kubernetes.io/name: {{ include "korecube.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}
