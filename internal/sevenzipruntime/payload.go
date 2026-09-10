package sevenzipruntime

import (
	"crypto/sha256"
	"fmt"

	"github.com/hackycy/hackycy-cli/internal/sevenzipmanifest"
)

type PayloadFile struct {
	Metadata sevenzipmanifest.File
	Bytes    []byte
}

type Payload struct {
	Artifact sevenzipmanifest.Artifact
	Files    []PayloadFile
}

func Current() Payload {
	return currentPayload()
}

func (payload Payload) Verify() error {
	if len(payload.Files) != len(payload.Artifact.Files) {
		return fmt.Errorf("embedded 7-Zip runtime for %s is incomplete", payload.Artifact.Target)
	}
	for index, file := range payload.Files {
		expected := payload.Artifact.Files[index]
		if file.Metadata != expected {
			return fmt.Errorf("embedded 7-Zip runtime for %s has an unexpected file manifest", payload.Artifact.Target)
		}
		actual := digest(file.Bytes)
		if actual != expected.SHA256 {
			return fmt.Errorf("embedded 7-Zip runtime file %s failed SHA-256 verification", expected.Filename)
		}
	}
	return nil
}

func digest(bytes []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(bytes))
}
