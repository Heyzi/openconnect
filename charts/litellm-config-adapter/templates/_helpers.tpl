{{- define "adapter.name" -}}litellm-config-adapter{{- end }}
{{- define "adapter.fullname" -}}{{ include "adapter.name" . }}{{- end }}
{{- define "adapter.labels" -}}
app.kubernetes.io/name: {{ include "adapter.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}
