{{- define "service.name" -}}
{{- default .Chart.Name .Values.nameOverride -}}
{{- end -}}

{{- define "service.fullname" -}}
{{- default .Release.Name .Values.nameOverride -}}
{{- end -}}

{{- define "service.labels" -}}
app.kubernetes.io/name: {{ include "service.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}
