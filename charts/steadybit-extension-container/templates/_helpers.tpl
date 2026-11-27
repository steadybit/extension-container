
{{/*
checks the .Values.containerRuntime for valid values
*/}}
{{- define "containerEngine.valid" -}}
{{- $valid := keys .Values.containerEngines | sortAlpha -}}
{{- if has .Values.container.runtime $valid -}}
{{- .Values.container.runtime  -}}
{{- else if has .Values.container.engine $valid -}}
{{- .Values.container.engine  -}}
{{- else -}}
{{- fail (printf "unknown container.engine: %v (must be one of %s)" .Values.container.engine (join ", " $valid)) -}}
{{- end -}}
{{- end -}}

{{- /*
ociRuntime.root will render the root for the selected container runtime
*/}}
{{- define "ociRuntime.get" -}}
{{- $top := index . 0 -}}
{{- $field := index . 1 -}}
{{- $engine := (include "containerEngine.valid" $top )  -}}
{{- $engineValues := get $top.Values.containerEngines $engine  -}}
{{- index $engineValues.ociRuntime $field -}}
{{- end -}}

{{- /*
containerEngines.socket will render the socket for the selected container runtime
*/}}
{{- define "containerEngine.socket" -}}
{{- $engine := (include "containerEngine.valid" . )  -}}
{{- $engineValues := get .Values.containerEngines $engine  -}}
{{- $engineValues.socket -}}
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

{{- /*
steadybit-extension-container.podAnnotations renders .Values.podAnnotations together with the annotations that opt
the pod out of Dynatrace Operator injection, unless .Values.dynatrace.inject is true. Keys set in podAnnotations win.
*/}}
{{- define "steadybit-extension-container.podAnnotations" -}}
{{- $annotations := dict -}}
{{- if ne (toString (dig "inject" false (.Values.dynatrace | default dict))) "true" -}}
{{- $_ := set $annotations "dynatrace.com/inject" "false" -}}
{{- $_ := set $annotations "oneagent.dynatrace.com/inject" "false" -}}
{{- $_ := set $annotations "metadata-enrichment.dynatrace.com/inject" "false" -}}
{{- end -}}
{{- range $key, $value := (.Values.podAnnotations | default dict) -}}
{{- $_ := set $annotations $key $value -}}
{{- end -}}
{{- range $key, $value := $annotations }}
{{ $key | quote }}: {{ ternary "" (toString $value) (kindIs "invalid" $value) | quote }}
{{- end -}}
{{- end -}}
