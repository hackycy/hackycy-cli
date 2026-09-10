//go:build windows

package updater

import "os/exec"

func configureDetachedCommand(command *exec.Cmd) {}
