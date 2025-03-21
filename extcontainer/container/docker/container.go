// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package docker

import (
	typecontainer "github.com/docker/docker/api/types/container"
)

// container implements the types.Container interface for Docker
type container struct {
	id        string
	names     []string
	imageName string
	labels    map[string]string
}

func newContainer(c typecontainer.Summary) *container {
	return &container{
		id:        c.ID,
		names:     c.Names,
		imageName: c.Image,
		labels:    c.Labels,
	}
}

func newContainerFromInspect(c typecontainer.InspectResponse) *container {
	return &container{
		id:        c.ID,
		names:     []string{c.Name},
		imageName: c.Image,
		labels:    c.Config.Labels,
	}
}

func (c *container) Id() string {
	return c.id
}

func (c *container) Name() string {
	if len(c.names) == 0 {
		return ""
	}
	return c.names[0]
}

func (c *container) ImageName() string {
	return c.imageName
}

func (c *container) Labels() map[string]string {
	return c.labels
}
