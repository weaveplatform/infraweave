{{/*
The release's own objects carry its name, so several releases share a
namespace. The objects in each watch namespace have fixed names instead:
Helm refuses a second release that renders one of them, which keeps every
watch namespace to one controller.
*/}}
{{- define "workers.name" -}}
{{- printf "%s-worker-controller" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "workers.uninstallName" -}}
{{- printf "%s-worker-uninstall" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "workers.labels" -}}
app.kubernetes.io/name: infraweave-workers
app.kubernetes.io/instance: {{ .Release.Name | quote }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service | quote }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | quote }}
{{- end -}}

{{- define "workers.selector" -}}
app.kubernetes.io/name: infraweave-workers
app.kubernetes.io/instance: {{ .Release.Name | quote }}
app.kubernetes.io/component: worker-controller
{{- end -}}

{{/* The label selector of the WorkerPools this release owns. */}}
{{- define "workers.poolSelector" -}}
{{- printf "app.kubernetes.io/name=infraweave-workers,app.kubernetes.io/instance=%s" .Release.Name -}}
{{- end -}}

{{- define "workers.image" -}}
{{- printf "%s@%s" .Values.controller.image.repository .Values.controller.image.digest -}}
{{- end -}}

{{- define "workers.bootstrapImage" -}}
{{- if .Values.bootstrapImage.digest -}}
{{- printf "%s@%s" .Values.bootstrapImage.repository .Values.bootstrapImage.digest -}}
{{- else -}}
{{- include "workers.image" . -}}
{{- end -}}
{{- end -}}

{{- define "workers.leaseName" -}}
{{- include "workers.name" . -}}
{{- end -}}

{{/* The paths the controller's mounted inputs appear at. */}}
{{- define "workers.configPath" -}}/etc/infraweave/worker-controller.yaml{{- end -}}
{{- define "workers.caPath" -}}/etc/infraweave/trust/ca.pem{{- end -}}
{{- define "workers.policyPath" -}}/etc/infraweave/policy/initialization.rego{{- end -}}

{{/*
workers.validate fails the render on values the schema cannot judge
alone. Online, it also requires every referenced Secret and ConfigMap
key to exist, so a missing reference fails the install instead of
leaving a Pod that cannot start; offline, as for helm template, lookup
answers nothing and those checks are skipped.
*/}}
{{- define "workers.validate" -}}
{{- $watched := .Values.watchNamespaces -}}
{{- $managed := false -}}
{{- $public := 0 -}}
{{- $seen := dict -}}
{{- range .Values.pools -}}
{{- if not (has .namespace $watched) -}}
{{- fail (printf "pools: %s/%s is in a namespace watchNamespaces does not name" .namespace .name) -}}
{{- end -}}
{{- $key := printf "%s/%s" .namespace .name -}}
{{- if hasKey $seen $key -}}
{{- fail (printf "pools: %s is named twice" $key) -}}
{{- end -}}
{{- $_ := set $seen $key true -}}
{{- if eq .spec.mode "Managed" -}}
{{- $managed = true -}}
{{- else if eq .spec.mode "Public" -}}
{{- $public = add1 $public -}}
{{- end -}}
{{- end -}}
{{- if and $managed (not .Values.managementCredentialsRef) -}}
{{- fail "managementCredentialsRef: Managed pools need the Secret of the worker-pool-controller API key" -}}
{{- end -}}
{{- if and .Values.managementCredentialsRef (gt (len .Values.pools) 0) (eq $public (len .Values.pools)) -}}
{{- fail "managementCredentialsRef: a release of only Public pools holds no management API key" -}}
{{- end -}}
{{- with .Values.managementCredentialsRef -}}
{{- if eq .idKey .secretKey -}}
{{- fail "managementCredentialsRef: idKey and secretKey must be distinct keys" -}}
{{- end -}}
{{- end -}}
{{- if and .Values.bootstrapImage.digest (not .Values.bootstrapImage.repository) -}}
{{- fail "bootstrapImage: a digest needs its repository" -}}
{{- end -}}
{{- if lookup "v1" "Namespace" "" .Release.Namespace -}}
{{- $release := .Release.Namespace -}}
{{- with .Values.managementCredentialsRef -}}
{{- $ref := . -}}
{{- range $watched -}}
{{- include "workers.requireKeys" (list "Secret" . $ref.name (list $ref.idKey $ref.secretKey) "managementCredentialsRef") -}}
{{- end -}}
{{- end -}}
{{- with .Values.trust.caSecretRef -}}
{{- include "workers.requireKeys" (list "Secret" $release .name (list .key) "trust.caSecretRef") -}}
{{- end -}}
{{- with .Values.initializationPolicyRef -}}
{{- include "workers.requireKeys" (list .kind $release .name (list .key) "initializationPolicyRef") -}}
{{- end -}}
{{- range .Values.pools -}}
{{- $keys := list "privateKey" -}}
{{- if ne .spec.mode "Managed" -}}
{{- $keys = list "privateKey" "token" -}}
{{- end -}}
{{- include "workers.requireKeys" (list "Secret" .namespace .spec.credentialsRef.name $keys (printf "pools: %s/%s credentialsRef" .namespace .name)) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/* workers.requireKeys fails unless the kind namespace/name holds every key. */}}
{{- define "workers.requireKeys" -}}
{{- $kind := index . 0 -}}
{{- $namespace := index . 1 -}}
{{- $name := index . 2 -}}
{{- $keys := index . 3 -}}
{{- $what := index . 4 -}}
{{- $found := lookup "v1" $kind $namespace $name -}}
{{- if not $found -}}
{{- fail (printf "%s: the %s %s/%s does not exist" $what $kind $namespace $name) -}}
{{- end -}}
{{- $data := dict -}}
{{- if $found.data -}}
{{- $data = $found.data -}}
{{- end -}}
{{- range $keys -}}
{{- if not (hasKey $data .) -}}
{{- fail (printf "%s: the %s %s/%s has no %s key" $what $kind $namespace $name .) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/* workers.egress renders egress rules as NetworkPolicy egress entries. */}}
{{- define "workers.egress" -}}
{{- range . }}
- to:
    - ipBlock:
        cidr: {{ .cidr | quote }}
        {{- with .except }}
        except:
          {{- toYaml . | nindent 10 }}
        {{- end }}
  {{- with .ports }}
  ports:
    {{- range . }}
    - port: {{ .port }}
      protocol: {{ .protocol }}
      {{- with .endPort }}
      endPort: {{ . }}
      {{- end }}
    {{- end }}
  {{- end }}
{{- end }}
{{- end -}}

{{/*
workers.config is the controller's worker_controller configuration. It
holds references and paths only: no Secret value is ever rendered.
*/}}
{{- define "workers.config" -}}
worker_controller:
  watch_namespaces:
    {{- toYaml .Values.watchNamespaces | nindent 4 }}
  cluster_id: {{ .Values.clusterID | quote }}
  {{- with .Values.managementCredentialsRef }}
  management_credentials_ref:
    name: {{ .name | quote }}
    id_key: {{ .idKey | quote }}
    secret_key: {{ .secretKey | quote }}
  {{- end }}
  leader_election:
    enabled: true
    name: {{ include "workers.leaseName" . | quote }}
    namespace: {{ .Release.Namespace | quote }}
  bootstrap_image: {{ include "workers.bootstrapImage" . | quote }}
  cache_dir: /var/cache/infraweave
  {{- with .Values.toolMirror }}
  tool_mirror: {{ . | quote }}
  {{- end }}
  {{- if .Values.trust.caSecretRef }}
  ca_file: {{ include "workers.caPath" . | quote }}
  {{- end }}
  admin_listen: ":9091"
  {{- if .Values.initializationPolicyRef }}
  initialization_policy: {{ include "workers.policyPath" . | quote }}
  {{- end }}
  initialization_timeout: {{ printf "%ds" (int .Values.initializationTimeoutSeconds) | quote }}
  cleanup_timeout: {{ printf "%ds" (int .Values.controller.cleanupTimeoutSeconds) | quote }}
  log:
    level: {{ .Values.controller.log.level | quote }}
    format: {{ .Values.controller.log.format | quote }}
{{- end -}}
