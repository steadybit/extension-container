
{{/*
checks the .Values.containerRuntime for valid values
*/}}
{{- define "containerRuntime.valid" -}}
{{- $valid := keys .Values.containerRuntimes | sortAlpha -}}
{{- $runtime := .Values.container.runtime -}}
{{- if has $runtime $valid -}}
{{- $runtime  -}}
{{- else -}}
{{- fail (printf "unknown container runtime: %v (must be one of %s)" $runtime (join ", " $valid)) -}}
{{- end -}}
{{- end -}}


{{- /*
containerRuntime.runcRoot will render the runcRoot for the selected container runtime
*/}}
{{- define "containerRuntime.runcRoot" -}}
{{- $runtime := (include "containerRuntime.valid" . )  -}}
{{- $runtimeValues := get .Values.containerRuntimes $runtime  -}}
{{- $runtimeValues.runcRoot -}}
{{- end -}}

{{- /*
containerRuntime.socket will render the socket for the selected container runtime
*/}}
{{- define "containerRuntime.socket" -}}
{{- $runtime := (include "containerRuntime.valid" . )  -}}
{{- $runtimeValues := get .Values.containerRuntimes $runtime  -}}
{{- $runtimeValues.socket -}}
{{- end -}}

{{- /*
will omit attribute from the passed in object depending on the KubeVersion
*/}}
{{- define "omitForKuberVersion" -}}
{{- $top := index . 0 -}}
{{- $versionConstraint := index . 1 -}}
{{- $dict := index . 2 -}}
{{- $toOmit := index . 3 -}}
{{- if semverCompare $versionConstraint $top.Capabilities.KubeVersion.Version -}}
{{- $dict = omit $dict $toOmit -}}
{{- end -}}
{{- $dict | toYaml -}}
{{- end -}}

