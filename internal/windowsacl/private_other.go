//go:build !windows

package windowsacl

func RestrictPrivatePath(string) error {
	return nil
}

func VerifyPrivatePath(string) error {
	return nil
}
