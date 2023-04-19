// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package containerd

import cdcontainers "github.com/containerd/containerd/containers"

// Container implements the engines.Container interface for containerd
type Container struct {
	info cdcontainers.Container
}

func (c *Container) Id() string {
	return c.info.ID
}

func (c *Container) Names() []string {
	return make([]string, 0)
}

func (c *Container) ImageName() string {
	return c.info.Image
}

func (c *Container) Labels() map[string]string {
	return c.info.Labels
}
