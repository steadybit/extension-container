// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package crio

import runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

// Container implements the types.Container interface for CRI
type container struct {
	id        string
	name      string
	imageName string
	labels    map[string]string
}

func newContainer(c *runtime.Container) *container {
	return &container{
		id:        c.Id,
		name:      c.Metadata.Name,
		imageName: c.Image.Image,
		labels:    c.Labels,
	}
}

func (c *container) Id() string {
	return c.id
}

func (c *container) Name() string {
	return c.name
}

func (c *container) ImageName() string {
	return c.imageName
}

func (c *container) Labels() map[string]string {
	return c.labels
}
