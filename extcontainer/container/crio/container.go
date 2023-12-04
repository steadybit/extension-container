// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package crio

import runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

// Container implements the types.Container interface for CRI
type Container struct {
	container *runtime.Container
}

func (c *Container) Id() string {
	return c.container.Id
}

func (c *Container) Name() string {
	return c.container.Metadata.Name
}

func (c *Container) ImageName() string {
	return c.container.Image.Image
}

func (c *Container) Labels() map[string]string {
	return c.container.Labels
}
