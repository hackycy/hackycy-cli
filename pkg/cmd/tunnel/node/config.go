package node

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

type Input struct {
	ManagementBindAddress *string
	ManagementPort        *string
	DataDir               *string
}

type Config struct {
	ManagementBindAddress string
	ManagementPort        int
	DataDir               string
}

type Environment func(string) (string, bool)

func ResolveConfig(input Input, environment Environment) (Config, error) {
	return resolveConfig(input, environment, defaultDataDirectory)
}

func resolveConfig(input Input, environment Environment, defaultDirectory func() (string, error)) (Config, error) {
	if environment == nil {
		environment = os.LookupEnv
	}
	bind := optionValue(input.ManagementBindAddress, environment, "YCY_TUNNEL_NODE_MANAGEMENT_BIND_ADDRESS", "0.0.0.0")
	if net.ParseIP(bind) == nil {
		return Config{}, fmt.Errorf("Node management bind address must be an IP address")
	}
	portText := optionValue(input.ManagementPort, environment, "YCY_TUNNEL_NODE_MANAGEMENT_PORT", "7600")
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 || strings.TrimSpace(portText) != portText {
		return Config{}, fmt.Errorf("Node management port must be an integer from 1 through 65535")
	}
	directory := optionValue(input.DataDir, environment, "YCY_TUNNEL_NODE_DATA_DIR", "")
	if input.DataDir == nil {
		if _, set := environment("YCY_TUNNEL_NODE_DATA_DIR"); !set {
			directory, err = defaultDirectory()
			if err != nil {
				return Config{}, err
			}
		}
	}
	if strings.TrimSpace(directory) == "" {
		return Config{}, fmt.Errorf("Node data directory must not be empty")
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return Config{}, fmt.Errorf("resolve Node data directory: %w", err)
	}
	return Config{ManagementBindAddress: bind, ManagementPort: port, DataDir: directory}, nil
}

func optionValue(flag *string, environment Environment, name, fallback string) string {
	if flag != nil {
		return *flag
	}
	if value, ok := environment(name); ok {
		return value
	}
	return fallback
}

func defaultDataDirectory() (string, error) {
	root, err := tunnelruntime.StateRoot(os.Getenv, os.UserHomeDir, runtime.GOOS)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "ycy", "tunnel", "node"), nil
}
