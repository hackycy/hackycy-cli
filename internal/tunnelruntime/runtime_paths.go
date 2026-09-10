package tunnelruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// DefaultFRPRuntimeDirectory returns the shared managed FRP runtime location
// for the current process environment and platform.
func DefaultFRPRuntimeDirectory() (string, error) {
	return managedFRPRuntimeDirectory(os.Getenv, os.UserHomeDir, runtime.GOOS)
}

func managedFRPRuntimeDirectory(environment func(string) string, userHomeDirectory func() (string, error), platform string) (string, error) {
	if environment == nil {
		environment = os.Getenv
	}
	if environment("YCY_TUNNEL_DOCKER") == "1" {
		return filepath.Join("/opt", "ycy", "frp", FRPVersion), nil
	}
	stateRoot, err := StateRoot(environment, userHomeDirectory, platform)
	if err != nil {
		return "", err
	}
	return filepath.Join(stateRoot, "ycy", "frp", FRPVersion), nil
}

// StateRoot resolves the shared Tunnel state root for an explicit environment
// and platform. Server and client leaves append their own private subpaths.
func StateRoot(environment func(string) string, userHomeDirectory func() (string, error), platform string) (string, error) {
	if environment == nil {
		environment = os.Getenv
	}
	if platform == "windows" {
		if root := environment("LOCALAPPDATA"); root != "" {
			return root, nil
		}
	}
	if platform == "linux" {
		if root := environment("XDG_STATE_HOME"); root != "" {
			return root, nil
		}
	}
	if userHomeDirectory == nil {
		userHomeDirectory = os.UserHomeDir
	}
	home, err := userHomeDirectory()
	if err != nil {
		return "", fmt.Errorf("resolve Tunnel state root: %w", err)
	}
	switch platform {
	case "windows":
		return filepath.Join(home, "AppData", "Local"), nil
	case "darwin":
		return filepath.Join(home, "Library", "Application Support"), nil
	default:
		return filepath.Join(home, ".local", "state"), nil
	}
}
