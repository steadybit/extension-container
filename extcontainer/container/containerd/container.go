// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package containerd

import (
	cdcontainers "github.com/containerd/containerd/containers"
)

// Container implements the engines.Container interface for containerd
type container struct {
	id        string
	imageName string
	labels    map[string]string
}

func newContainer(c cdcontainers.Container) *container {
	return &container{
		id:        c.ID,
		imageName: c.Image,
		labels:    c.Labels,
	}
}

func (c *container) Id() string {
	return c.id
}

func (c *container) Name() string {
	return ""
}

func (c *container) ImageName() string {
	return c.imageName
}

func (c *container) Labels() map[string]string {
	return c.labels
}
