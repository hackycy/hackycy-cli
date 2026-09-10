package appconfig

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	darwinMachineIDPattern  = regexp.MustCompile(`"IOPlatformUUID"\s*=\s*"([^"]+)"`)
	windowsMachineIDPattern = regexp.MustCompile(`(?mi)^\s*MachineGuid\s+REG_SZ\s+(\S+)\s*$`)
)

func machineIDWithFallback(native func() (string, error), hostname func() (string, error), username func() (string, error)) (string, error) {
	if value, err := native(); err == nil && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value), nil
	}
	host, hostErr := hostname()
	if hostErr != nil {
		return "", fmt.Errorf("resolve machine ID fallback hostname: %w", hostErr)
	}
	user, userErr := username()
	if userErr != nil {
		return "", fmt.Errorf("resolve machine ID fallback username: %w", userErr)
	}
	if strings.TrimSpace(host) == "" || strings.TrimSpace(user) == "" {
		return "", errors.New("resolve machine ID fallback: empty hostname or username")
	}
	return host + "-" + user, nil
}

func parseDarwinMachineID(output string) (string, error) {
	match := darwinMachineIDPattern.FindStringSubmatch(output)
	if len(match) != 2 || strings.TrimSpace(match[1]) == "" {
		return "", errors.New("IOPlatformUUID is unavailable")
	}
	return match[1], nil
}

func parseLinuxMachineID(contents string) (string, error) {
	value := strings.TrimSpace(contents)
	if value == "" {
		return "", errors.New("machine-id is unavailable")
	}
	return value, nil
}

func parseWindowsMachineID(output string) (string, error) {
	match := windowsMachineIDPattern.FindStringSubmatch(output)
	if len(match) != 2 || strings.TrimSpace(match[1]) == "" {
		return "", errors.New("MachineGuid is unavailable")
	}
	return match[1], nil
}
