/*
 * Copyright 2023 steadybit GmbH. All rights reserved.
 */

package utils

import (
	"github.com/rs/zerolog/log"
	"os"
	"path/filepath"
	"sync"
)

var (
	sidecarImage     string
	sidecarImageOnce sync.Once
)

func SidecarImagePath() string {
	sidecarImageOnce.Do(func() {
		if _, err := os.Stat("sidecar"); err == nil {
			sidecarImage = "sidecar"
			return
		}

		if _, err := os.Stat("sidecar.tar"); err == nil {
			sidecarImage = "sidecar.tar"
			return
		}

		if executable, err := os.Executable(); err == nil {
			executableDir := filepath.Dir(filepath.Clean(executable))

			candidate := filepath.Join(executableDir, "sidecar")
			if _, err := os.Stat(candidate); err == nil {
				sidecarImage = candidate
				return
			}

			candidate = filepath.Join(executableDir, "sidecar.tar")
			if _, err := os.Stat(candidate); err == nil {
				sidecarImage = candidate
				return
			}

			log.Fatal().Msg("failed to find sidecar image")
		}
	})
	return sidecarImage
}
