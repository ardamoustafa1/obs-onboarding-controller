{{- define "obs-onboarding-controller.fullname" -}}
{{ .Release.Name }}-obs-onboarding-controller
{{- end -}}

{{- define "obs-onboarding-controller.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "obs-onboarding-controller.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "obs-onboarding-controller.labels" -}}
app.kubernetes.io/name: obs-onboarding-controller
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}
