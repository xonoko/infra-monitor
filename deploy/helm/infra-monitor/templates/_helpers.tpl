{{- define "infra-monitor.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "infra-monitor.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "infra-monitor.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "infra-monitor.selectorLabels" -}}
app.kubernetes.io/name: {{ include "infra-monitor.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "infra-monitor.labels" -}}
helm.sh/chart: {{ include "infra-monitor.chart" . }}
{{ include "infra-monitor.selectorLabels" . }}
app.kubernetes.io/version: {{ (default .Chart.AppVersion .Values.image.tag) | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "infra-monitor.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "infra-monitor.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "infra-monitor.secretName" -}}
{{- if .Values.secrets.existingSecret -}}
{{- .Values.secrets.existingSecret -}}
{{- else if .Values.secrets.create -}}
{{- include "infra-monitor.fullname" . -}}
{{- end -}}
{{- end -}}

{{- define "infra-monitor.cnpgName" -}}
{{- printf "%s-db" (include "infra-monitor.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Database section of the rendered config. */}}
{{- define "infra-monitor.databaseConfig" -}}
{{- $db := dict "port" (int .Values.database.port) "max_conns" (int .Values.database.maxConns) "migrate_on_start" .Values.database.migrateOnStart -}}
{{- if .Values.cnpg.enabled -}}
{{- $_ := set $db "host" (printf "%s-rw" (include "infra-monitor.cnpgName" .)) -}}
{{- $_ := set $db "name" .Values.database.name -}}
{{- $_ := set $db "user" .Values.database.user -}}
{{- $_ := set $db "sslmode" "require" -}}
{{- $_ := set $db "password_file" "/etc/infra-monitor/db/password" -}}
{{- else if .Values.database.dsnKey -}}
{{- $_ := set $db "dsn_file" (printf "%s/%s" .Values.secrets.mountPath .Values.database.dsnKey) -}}
{{- else -}}
{{- $_ := set $db "host" .Values.database.host -}}
{{- $_ := set $db "name" .Values.database.name -}}
{{- $_ := set $db "user" .Values.database.user -}}
{{- $_ := set $db "sslmode" .Values.database.sslmode -}}
{{- if .Values.database.passwordKey -}}
{{- $_ := set $db "password_file" (printf "%s/%s" .Values.secrets.mountPath .Values.database.passwordKey) -}}
{{- end -}}
{{- end -}}
{{- $override := dict -}}
{{- if .Values.config.database -}}{{- $override = .Values.config.database -}}{{- end -}}
{{- toYaml (mergeOverwrite $db $override) -}}
{{- end -}}

{{- define "infra-monitor.config" -}}
{{- $cfg := deepCopy .Values.config -}}
{{- $_ := set $cfg "database" (include "infra-monitor.databaseConfig" . | fromYaml) -}}
{{- if .Values.approvedActions.enabled -}}
{{- $actions := default dict (get $cfg "actions") -}}
{{- $generic := default dict (get $actions "generic") -}}
{{- $_ := set $generic "enabled" true -}}
{{- $_ := set $generic "approved_service_account" (printf "system:serviceaccount:%s:%s" .Release.Namespace (include "infra-monitor.approvedServiceAccountName" .)) -}}
{{- $_ := set $generic "exec_enabled" .Values.approvedActions.exec -}}
{{- $_ := set $actions "generic" $generic -}}
{{- $_ := set $cfg "actions" $actions -}}
{{- end -}}
{{- if .Values.tls.enabled -}}
{{- $server := default dict (get $cfg "server") -}}
{{- $_ := set $server "tls" (dict "cert_file" (printf "%s/tls.crt" .Values.tls.mountPath) "key_file" (printf "%s/tls.key" .Values.tls.mountPath)) -}}
{{- $_ := set $cfg "server" $server -}}
{{- if and .Values.tls.trustCA (not (get $cfg "ca_file")) -}}
{{- $_ := set $cfg "ca_file" (printf "%s/ca.crt" .Values.tls.mountPath) -}}
{{- end -}}
{{- end -}}
{{- toYaml $cfg -}}
{{- end -}}

{{- define "infra-monitor.tlsSecretName" -}}
{{- default (printf "%s-tls" (include "infra-monitor.fullname" .)) .Values.tls.secretName -}}
{{- end -}}

{{- define "infra-monitor.scheme" -}}
{{- if .Values.tls.enabled }}HTTPS{{ else }}HTTP{{ end -}}
{{- end -}}

{{/* Render a probe, forcing httpGet.scheme from tls.enabled. Args: (list probe scheme) */}}
{{- define "infra-monitor.probe" -}}
{{- $p := deepCopy (index . 0) -}}
{{- if hasKey $p "httpGet" -}}
{{- $_ := set $p.httpGet "scheme" (index . 1) -}}
{{- end -}}
{{- toYaml $p -}}
{{- end -}}

{{- define "infra-monitor.approvedServiceAccountName" -}}
{{- default (printf "%s-approved" (include "infra-monitor.fullname" .)) .Values.approvedActions.serviceAccountName -}}
{{- end -}}
