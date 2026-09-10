//go:build windows

package appconfig

import "os/exec"

func nativeMachineID() (string, error) {
	output, err := exec.Command("reg", "query", `HKLM\SOFTWARE\Microsoft\Cryptography`, "/v", "MachineGuid").Output()
	if err != nil {
		return "", err
	}
	return parseWindowsMachineID(string(output))
}
