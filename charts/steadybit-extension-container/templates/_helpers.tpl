
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
{{- $engine := (include "containerEngine.valid" (index . 0) )  -}}
{{- $engineValues := get .Values.containerEngines $engine  -}}
{{- index $engineValues.ociRuntime (index . 1) -}}
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

