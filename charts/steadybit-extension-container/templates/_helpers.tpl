
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
containerRuntime.volumeMounts will render pod volume mounts(without indentation) for the selected container runtime
*/}}
{{- define "containerRuntime.volumeMounts" -}}
{{- $runtime := (include "containerRuntime.valid" . )  -}}
{{- $runtimeValues := get .Values.containerRuntimes $runtime  -}}
- name: "runtime-socket"
  mountPath: "{{ $runtimeValues.socket }}"
- name: "runtime-runc-root"
  mountPath: "{{ $runtimeValues.runcRoot }}"
{{- end -}}

{{- /*
containerRuntime.volumes will render pod volumes (without indentation) for the selected container runtime
*/}}
{{- define "containerRuntime.volumes" -}}
{{- $runtime := (include "containerRuntime.valid" . )  -}}
{{- $runtimeValues := get .Values.containerRuntimes $runtime  -}}
- name: "runtime-socket"
  hostPath:
    path: "{{ $runtimeValues.socket }}"
    type: Socket
- name: "runtime-runc-root"
  hostPath:
    path: "{{ $runtimeValues.runcRoot }}"
    type: Directory
{{- end -}}
