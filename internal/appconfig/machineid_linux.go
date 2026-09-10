//go:build linux

package appconfig

import "os"

func nativeMachineID() (string, error) {
	contents, err := os.ReadFile("/etc/machine-id")
	if err != nil {
		return "", err
	}
	return parseLinuxMachineID(string(contents))
}
