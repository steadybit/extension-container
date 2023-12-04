// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package docker

import "github.com/docker/docker/api/types"

// Container implements the types.Container interface for Docker
type Container struct {
	docker types.Container
}

func (c *Container) Id() string {
	return c.docker.ID
}

func (c *Container) Name() string {
	if len(c.docker.Names) == 0 {
		return ""
	}
	return c.docker.Names[0]
}

func (c *Container) ImageName() string {
	return c.docker.Image
}

func (c *Container) Labels() map[string]string {
	return c.docker.Labels
}
