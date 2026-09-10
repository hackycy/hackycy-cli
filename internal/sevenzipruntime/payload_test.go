package sevenzipruntime

import (
	"runtime"
	"testing"
)

func TestCurrentPayloadMatchesItsPinnedManifest(t *testing.T) {
	payload := Current()
	if payload.Artifact.Target != runtime.GOOS+"-"+runtime.GOARCH {
		t.Fatalf("payload target = %q", payload.Artifact.Target)
	}
	if err := payload.Verify(); err != nil {
		t.Fatal(err)
	}
	for _, file := range payload.Files {
		if len(file.Bytes) == 0 {
			t.Fatalf("embedded file %s is empty", file.Metadata.Filename)
		}
	}
}

func TestPayloadVerificationRejectsChangedBytes(t *testing.T) {
	payload := Current()
	broken := append([]byte(nil), payload.Files[0].Bytes...)
	broken[0] ^= 1
	payload.Files[0].Bytes = broken
	if err := payload.Verify(); err == nil {
		t.Fatal("Verify() accepted changed embedded bytes")
	}
}
