package utils

import (
	"github.com/steadybit/extension-kit/extutil"
	"os"
)

func GetOwnPort() uint16 {
	envPort := os.Getenv("STEADYBIT_EXTENSION_PORT")
	if envPort != "" {
		uInt := extutil.ToUInt(envPort)
		if uInt != 0 {
			return uint16(uInt)
		}
	}
	return 8080
}

func GetOwnHealthPort() uint16 {
	envPort := os.Getenv("STEADYBIT_EXTENSION_HEALTH_PORT")
	if envPort != "" {
		uInt := extutil.ToUInt(envPort)
		if uInt != 0 {
			return uint16(uInt)
		}
	}
	return 8081
}
