// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package docker

import "github.com/docker/docker/api/types"

// Container implements the types.Container interface for Docker
type Container struct {
	id        string
	names     []string
	imageName string
	labels    map[string]string
}

func newContainer(c types.Container) *Container {
	return &Container{
		id:        c.ID,
		names:     c.Names,
		imageName: c.Image,
		labels:    c.Labels,
	}
}

func (c *Container) Id() string {
	return c.id
}

func (c *Container) Name() string {
	if len(c.names) == 0 {
		return ""
	}
	return c.names[0]
}

func (c *Container) ImageName() string {
	return c.imageName
}

func (c *Container) Labels() map[string]string {
	return c.labels
}
