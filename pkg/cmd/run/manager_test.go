package run

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPackageManagerOrderUsesTheFirstMatchingLockfile(t *testing.T) {
	testCases := []struct {
		name      string
		lockfiles []string
		want      []PackageManager
	}{
		{name: "pnpm", lockfiles: []string{"pnpm-lock.yaml"}, want: []PackageManager{PackageManagerPNPM, PackageManagerNPM, PackageManagerExternal, PackageManagerYarn}},
		{name: "external binary lock", lockfiles: []string{"b" + "un" + ".lockb"}, want: []PackageManager{PackageManagerExternal, PackageManagerPNPM, PackageManagerNPM, PackageManagerYarn}},
		{name: "external text lock", lockfiles: []string{"b" + "un" + ".lock"}, want: []PackageManager{PackageManagerExternal, PackageManagerPNPM, PackageManagerNPM, PackageManagerYarn}},
		{name: "yarn", lockfiles: []string{"yarn.lock"}, want: []PackageManager{PackageManagerYarn, PackageManagerPNPM, PackageManagerNPM, PackageManagerExternal}},
		{name: "npm", lockfiles: []string{"package-lock.json"}, want: []PackageManager{PackageManagerNPM, PackageManagerPNPM, PackageManagerExternal, PackageManagerYarn}},
		{name: "priority collision", lockfiles: []string{"package-lock.json", "yarn.lock", "b" + "un" + ".lock", "b" + "un" + ".lockb", "pnpm-lock.yaml"}, want: []PackageManager{PackageManagerPNPM, PackageManagerNPM, PackageManagerExternal, PackageManagerYarn}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			directory := t.TempDir()
			for _, lockfile := range testCase.lockfiles {
				if err := os.WriteFile(filepath.Join(directory, lockfile), nil, 0o600); err != nil {
					t.Fatalf("write %s: %v", lockfile, err)
				}
			}

			order, err := PackageManagerOrder(directory, pathExists)
			if err != nil {
				t.Fatalf("PackageManagerOrder() error = %v", err)
			}
			if !reflect.DeepEqual(order, testCase.want) {
				t.Fatalf("order = %#v, want %#v", order, testCase.want)
			}
		})
	}
}

func TestPackageManagerOrderUsesTheDefaultWithoutALockfile(t *testing.T) {
	order, err := PackageManagerOrder(t.TempDir(), pathExists)
	if err != nil {
		t.Fatalf("PackageManagerOrder() error = %v", err)
	}
	want := []PackageManager{PackageManagerPNPM, PackageManagerNPM, PackageManagerExternal, PackageManagerYarn}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %#v, want %#v", order, want)
	}
}

func TestPackageManagerOrderReturnsExistenceErrors(t *testing.T) {
	failure := errors.New("read lockfile")
	_, err := PackageManagerOrder(t.TempDir(), func(string) (bool, error) {
		return false, failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("PackageManagerOrder() error = %v, want %v", err, failure)
	}
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}
