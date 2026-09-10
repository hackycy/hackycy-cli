package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUnixInstallerFixtureFreshReplacementAndRollback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix installer fixture")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is unavailable")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl is unavailable")
	}
	newBinary := []byte("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf '1.2.3\\n'; exit 0; fi\n")
	oldBinary := []byte("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf '1.0.0\\n'; exit 0; fi\n")
	newDigest := sha256Hex(newBinary)
	var payload = newBinary
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/latest":
			// Omit the asset digest to force the SHA256SUMS fallback.
			_, _ = io.WriteString(writer, `{"tag_name":"v1.2.3","assets":[{"name":"ycy-linux-x64"}]}`)
		case "/download/v1.2.3/SHA256SUMS":
			_, _ = io.WriteString(writer, newDigest+"  *ycy-linux-x64\n")
		case "/download/v1.2.3/ycy-linux-x64":
			_, _ = writer.Write(payload)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	root := filepath.Join(t.TempDir(), "profile with space-用户")
	installDir := filepath.Join(root, ".ycy-cli", "bin")
	environment := append(os.Environ(),
		"HOME="+root,
		"YCY_INSTALL_DIR="+installDir,
		"YCY_INSTALL_API_URL="+server.URL+"/latest",
		"YCY_INSTALL_DOWNLOAD_BASE="+server.URL+"/download",
		"YCY_INSTALL_OS=Linux",
		"YCY_INSTALL_ARCH=amd64",
		"YCY_INSTALL_SKIP_PATH=1",
	)
	rootPath := filepath.Join(repositoryRootForUpgradeTest(t), "scripts", "install.sh")
	command := exec.Command("bash", rootPath)
	command.Env = environment
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("fresh installer = %v\n%s", err, output)
	}
	installed := filepath.Join(installDir, "ycy")
	bytes, err := os.ReadFile(installed)
	if err != nil || string(bytes) != string(newBinary) {
		t.Fatalf("installed bytes = %q, %v", bytes, err)
	}
	foreignState := installed + ".update-state.json"
	if err := os.WriteFile(foreignState, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed, oldBinary, 0o755); err != nil {
		t.Fatal(err)
	}
	command = exec.Command("bash", rootPath)
	command.Env = environment
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("replacement installer = %v\n%s", err, output)
	}

	// A failed self-check must restore the prior stable target.
	payload = []byte("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf '9.9.9\\n'; exit 0; fi\n")
	// Keep the manifest digest synchronized through a second local server.
	rollbackServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/latest":
			_, _ = io.WriteString(writer, `{"tag_name":"v1.2.3","assets":[{"name":"ycy-linux-x64"}]}`)
		case "/download/v1.2.3/SHA256SUMS":
			_, _ = io.WriteString(writer, sha256Hex(payload)+"  ycy-linux-x64\n")
		case "/download/v1.2.3/ycy-linux-x64":
			_, _ = writer.Write(payload)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer rollbackServer.Close()
	rollbackEnv := append([]string{}, environment...)
	rollbackEnv = append(rollbackEnv, "YCY_INSTALL_API_URL="+rollbackServer.URL+"/latest", "YCY_INSTALL_DOWNLOAD_BASE="+rollbackServer.URL+"/download")
	command = exec.Command("bash", rootPath)
	command.Env = rollbackEnv
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("wrong-version installer unexpectedly succeeded: %s", output)
	}
	bytes, err = os.ReadFile(installed)
	if err != nil || string(bytes) != string(newBinary) {
		t.Fatalf("rollback bytes = %q, %v", bytes, err)
	}
	foreignBytes, err := os.ReadFile(foreignState)
	if err != nil || string(foreignBytes) != "preserve" {
		t.Fatalf("foreign installer state = %q, %v", foreignBytes, err)
	}
}

func TestInstallerScriptsExposeNativeArchitectureContract(t *testing.T) {
	root := repositoryRootForUpgradeTest(t)
	powershell, err := os.ReadFile(filepath.Join(root, "scripts", "install.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	contents := string(powershell)
	for _, required := range []string{"RuntimeInformation", "arm64", "YCY_INSTALL_API_URL", "YCY_INSTALL_DOWNLOAD_BASE", "YCY_INSTALL_DIR"} {
		if !strings.Contains(contents, required) {
			t.Fatalf("install.ps1 missing %q", required)
		}
	}
	if strings.Contains(contents, `$ArtifactName = "ycy-windows-x64.exe"`) {
		t.Fatal("install.ps1 still hard-codes Windows x64")
	}
	if _, err := ArtifactFor("windows", "amd64"); err != nil {
		t.Fatal(err)
	}
	if _, err := ArtifactFor("windows", "arm64"); err != nil {
		t.Fatal(err)
	}
}

func sha256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func repositoryRootForUpgradeTest(t *testing.T) string {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(workingDirectory, "..", ".."))
}
