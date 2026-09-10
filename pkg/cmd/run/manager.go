package run

import "path/filepath"

// PackageManager is one supported project package manager.
type PackageManager string

const (
	PackageManagerPNPM     PackageManager = "pnpm"
	PackageManagerNPM      PackageManager = "npm"
	PackageManagerExternal PackageManager = "b" + "un"
	PackageManagerYarn     PackageManager = "yarn"
)

var defaultPackageManagerOrder = []PackageManager{
	PackageManagerPNPM,
	PackageManagerNPM,
	PackageManagerExternal,
	PackageManagerYarn,
}

var lockfilePackageManagers = []struct {
	name    string
	manager PackageManager
}{
	{name: "pnpm-lock.yaml", manager: PackageManagerPNPM},
	{name: "b" + "un" + ".lockb", manager: PackageManagerExternal},
	{name: "b" + "un" + ".lock", manager: PackageManagerExternal},
	{name: "yarn.lock", manager: PackageManagerYarn},
	{name: "package-lock.json", manager: PackageManagerNPM},
}

// FileExists reports whether a project path exists.
type FileExists func(path string) (bool, error)

// PackageManagerOrder moves the first matching lockfile manager to the front.
func PackageManagerOrder(directory string, exists FileExists) ([]PackageManager, error) {
	for _, lockfile := range lockfilePackageManagers {
		found, err := exists(filepath.Join(directory, lockfile.name))
		if err != nil {
			return nil, err
		}
		if found {
			return preferredPackageManagerOrder(lockfile.manager), nil
		}
	}
	return append([]PackageManager(nil), defaultPackageManagerOrder...), nil
}

func preferredPackageManagerOrder(preferred PackageManager) []PackageManager {
	order := []PackageManager{preferred}
	for _, manager := range defaultPackageManagerOrder {
		if manager != preferred {
			order = append(order, manager)
		}
	}
	return order
}
