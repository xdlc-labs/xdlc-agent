{{- define "xdlc-agent.name" -}}
xdlc-agent
{{- end -}}

{{- define "xdlc-agent.fullname" -}}
{{ .Release.Name }}-xdlc-agent
{{- end -}}

{{- define "xdlc-agent.labels" -}}
app.kubernetes.io/name: {{ include "xdlc-agent.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
Image reference. image.digest wins over image.tag when set (immutable,
reproducible); image.tag defaults to the chart's appVersion rather than
"latest" so a release deploys the code it shipped with.
*/}}
{{- define "xdlc-agent.image" -}}
{{- if .Values.image.digest -}}
{{ .Values.image.repository }}@{{ .Values.image.digest }}
{{- else -}}
{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}
{{- end -}}
{{- end -}}

{{/*
Single-writer guard. The daemon takes bbolt's exclusive flock on
xdlc-agent-history.db (internal/store/audit.go), so a second replica
either crash-loops on the lock or, with persistence disabled, runs on a
divergent audit store. Fail at template time instead — a silent
replicaCount: 2 looks like HA and isn't. See
chart values (HA not implemented).
*/}}
{{- define "xdlc-agent.validateReplicas" -}}
{{- $n := int (default 1 .Values.replicaCount) -}}
{{- if gt $n 1 -}}
{{- fail (printf "xdlc-agent: replicaCount=%d is not supported — the daemon holds an exclusive bbolt lock on the audit DB (single writer) and the data PVC is ReadWriteOnce, so a second replica cannot start. Use replicaCount: 1; HA is not implemented; keep replicaCount: 1." $n) -}}
{{- end -}}
{{- end -}}

{{- define "xdlc-agent.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "xdlc-agent.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}
