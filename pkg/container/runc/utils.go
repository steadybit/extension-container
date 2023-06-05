// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package runc

import (
	"encoding/json"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/rs/zerolog/log"
	"os"
	"path/filepath"
)

type SpecEditor func(spec *specs.Spec)

func EditSpec(bundle string, editors ...SpecEditor) error {
	spec, err := readSpec(filepath.Join(bundle, "config.json"))
	if err != nil {
		return err
	}

	withDefaults(spec)

	for _, fn := range editors {
		fn(spec)
	}
	err = writeSpec(filepath.Join(bundle, "config.json"), spec)
	log.Trace().Str("bundle", bundle).Interface("spec", spec).Msg("written runc spec")
	return err
}

func readSpec(file string) (*specs.Spec, error) {
	content, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	var spec specs.Spec

	if err := json.Unmarshal(content, &spec); err != nil {
		return nil, err
	}

	return &spec, nil
}

func writeSpec(file string, spec *specs.Spec) error {
	content, err := json.MarshalIndent(spec, "", "\t")
	if err != nil {
		return err
	}
	return os.WriteFile(file, content, 0644)
}

func withDefaults(spec *specs.Spec) {
	spec.Root.Path = "rootfs"
	spec.Root.Readonly = true
	spec.Process.Terminal = false
}

func WithMountIfNotPresent(mount specs.Mount) SpecEditor {
	return func(spec *specs.Spec) {
		for _, m := range spec.Mounts {
			if m.Destination == mount.Destination {
				return
			}
		}
		spec.Mounts = append(spec.Mounts, mount)
	}
}

func WithHostname(hostname string) SpecEditor {
	return func(spec *specs.Spec) {
		spec.Hostname = hostname
	}
}

func WithAnnotations(annotations map[string]string) SpecEditor {
	return func(spec *specs.Spec) {
		spec.Annotations = annotations
	}
}

func WithProcessArgs(args ...string) SpecEditor {
	return func(spec *specs.Spec) {
		spec.Process.Args = args
	}
}
func WithProcessCwd(cwd string) SpecEditor {
	return func(spec *specs.Spec) {
		spec.Process.Cwd = cwd
	}
}

func WithCapabilities(caps ...string) SpecEditor {
	return func(spec *specs.Spec) {
		for _, c := range caps {
			spec.Process.Capabilities.Bounding = appendIfMissing(spec.Process.Capabilities.Bounding, c)
			spec.Process.Capabilities.Effective = appendIfMissing(spec.Process.Capabilities.Effective, c)
			spec.Process.Capabilities.Inheritable = appendIfMissing(spec.Process.Capabilities.Inheritable, c)
			spec.Process.Capabilities.Permitted = appendIfMissing(spec.Process.Capabilities.Effective, c)
			spec.Process.Capabilities.Ambient = appendIfMissing(spec.Process.Capabilities.Ambient, c)
		}
	}
}

func appendIfMissing(list []string, str string) []string {
	for _, item := range list {
		if item == str {
			return list
		}
	}
	return append(list, str)
}

func WithCgroupPath(cgroupPath, child string) SpecEditor {
	return func(spec *specs.Spec) {
		spec.Linux.CgroupsPath = filepath.Join(cgroupPath, child)
	}
}

func WithNamespaces(ns []specs.LinuxNamespace) SpecEditor {
	return func(spec *specs.Spec) {
		spec.Linux.Namespaces = ns

		for _, n := range ns {
			if n.Type == specs.MountNamespace {
				return
			}
		}
		spec.Linux.Namespaces = append(spec.Linux.Namespaces, specs.LinuxNamespace{Type: specs.MountNamespace})
	}
}

func WithSelectedNamespaces(ns []specs.LinuxNamespace, filter ...specs.LinuxNamespaceType) SpecEditor {
	return WithNamespaces(FilterNamespaces(ns, filter...))
}

func FilterNamespaces(ns []specs.LinuxNamespace, types ...specs.LinuxNamespaceType) []specs.LinuxNamespace {
	var result []specs.LinuxNamespace
	for _, n := range ns {
		for _, t := range types {
			if n.Type == t {
				result = append(result, n)
			}
		}
	}
	return result
}
