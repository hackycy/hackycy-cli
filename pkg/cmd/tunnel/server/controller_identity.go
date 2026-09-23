package server

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const controllerKeyFileName = "controller.key"

func loadControllerPublicKey(directory string, databaseExists bool) (string, error) {
	path := filepath.Join(directory, controllerKeyFileName)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if databaseExists {
			return "", fmt.Errorf("Tunnel Controller identity file is missing; restore the original identity file")
		}
		key, err := ecdh.X25519().GenerateKey(rand.Reader)
		if err != nil {
			return "", fmt.Errorf("generate Tunnel Controller identity: %w", err)
		}
		file, err := os.CreateTemp(directory, ".controller-key-")
		if err != nil {
			return "", fmt.Errorf("create Tunnel Controller identity file: %w", err)
		}
		defer os.Remove(file.Name())
		if err := file.Chmod(0o600); err != nil {
			_ = file.Close()
			return "", err
		}
		if _, err := file.Write(key.Bytes()); err != nil {
			_ = file.Close()
			return "", fmt.Errorf("write Tunnel Controller identity: %w", err)
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			return "", fmt.Errorf("sync Tunnel Controller identity: %w", err)
		}
		if err := file.Close(); err != nil {
			return "", err
		}
		if err := os.Rename(file.Name(), path); err != nil {
			return "", fmt.Errorf("publish Tunnel Controller identity: %w", err)
		}
		if runtime.GOOS != "windows" {
			directoryHandle, err := os.Open(directory)
			if err != nil {
				return "", fmt.Errorf("open Tunnel Controller identity directory: %w", err)
			}
			if err := directoryHandle.Sync(); err != nil {
				_ = directoryHandle.Close()
				return "", fmt.Errorf("sync Tunnel Controller identity directory: %w", err)
			}
			if err := directoryHandle.Close(); err != nil {
				return "", err
			}
		}
		return hex.EncodeToString(key.PublicKey().Bytes()), nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect Tunnel Controller identity: %w", err)
	}
	if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		return "", fmt.Errorf("Tunnel Controller identity file must be private and regular")
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read Tunnel Controller identity: %w", err)
	}
	key, err := ecdh.X25519().NewPrivateKey(bytes)
	if err != nil {
		return "", fmt.Errorf("Tunnel Controller identity file is invalid: %w", err)
	}
	return hex.EncodeToString(key.PublicKey().Bytes()), nil
}
