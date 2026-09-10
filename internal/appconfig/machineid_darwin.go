//go:build darwin

package appconfig

import "os/exec"

func nativeMachineID() (string, error) {
	output, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return "", err
	}
	return parseDarwinMachineID(string(output))
}
