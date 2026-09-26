package node

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

func TestNodeStateIdentityPersistsAndDirectoryIsExclusive(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "node")
	state, err := OpenState(directory)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, nodeID := state.Fingerprint(), state.nodeID
	if runtime.GOOS != "windows" {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			info, err := os.Stat(filepath.Join(state.directory, nodeDatabaseFile+suffix))
			if err != nil || info.Mode().Perm()&0o077 != 0 {
				t.Fatalf("Node database%s is not private while open: (%v, %v)", suffix, info, err)
			}
		}
	}
	if !strings.HasPrefix(fingerprint, "SHA256:") || len(fingerprint) != len("SHA256:")+43 {
		t.Fatalf("incomplete fingerprint %q", fingerprint)
	}
	if _, err := OpenState(directory); !errors.Is(err, tunnelruntime.ErrInstanceActive) {
		t.Fatalf("concurrent open = %v", err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	state, err = OpenState(directory)
	if err != nil {
		t.Fatal(err)
	}
	if state.Fingerprint() != fingerprint || state.nodeID != nodeID {
		t.Fatalf("identity changed across restart")
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		for _, name := range []string{"", nodeDatabaseFile} {
			info, err := os.Stat(filepath.Join(directory, "node-state-v1", name))
			if err != nil || info.Mode().Perm()&0o077 != 0 {
				t.Fatalf("%s is not private: (%v, %v)", name, info, err)
			}
		}
	}
}

func TestNodeStateRejectsDamagedOrIncompleteDirectoryWithoutReplacingIdentity(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "node")
	state, err := OpenState(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "node-state-v1", nodeDatabaseFile)
	if err := os.WriteFile(path, []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenState(directory); err == nil {
		t.Fatal("damaged database was accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("rejection changed database bytes: %v", err)
	}
	other := filepath.Join(t.TempDir(), "node")
	if err := os.Mkdir(other, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(other, "node-state-v1"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "node-state-v1", "node.sqlite-wal"), []byte("orphan"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenState(other); err == nil {
		t.Fatal("orphaned WAL was accepted")
	}
	if _, err := os.Stat(filepath.Join(other, "node-state-v1", nodeDatabaseFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing database was generated: %v", err)
	}
}

func TestNodeStateDoesNotReplaceIdentityWhenOnlyDatabaseIsLost(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "node")
	state, err := OpenState(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(filepath.Join(directory, "node-state-v1", nodeDatabaseFile+suffix)); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
	if _, err := OpenState(directory); err == nil {
		t.Fatal("missing database generated a replacement identity")
	}
	if _, err := os.Stat(filepath.Join(directory, "node-state-v1", nodeDatabaseFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing database was recreated: %v", err)
	}
}

func TestNodeStateRejectsIncompleteRuntimeWithoutChangingDatabase(t *testing.T) {
	for _, statement := range []string{`DELETE FROM runtime_state`, `UPDATE runtime_state SET highest_revision=1, highest_digest='wrong', candidate='{}' WHERE id=1`} {
		t.Run(statement, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "node")
			state, err := OpenState(directory)
			if err != nil {
				t.Fatal(err)
			}
			if err := state.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "node-state-v1", nodeDatabaseFile)
			db, err := sql.Open("sqlite3", nodeDatabaseURI(path))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(statement); err != nil {
				_ = db.Close()
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := OpenState(directory); err == nil {
				t.Fatal("damaged runtime was accepted")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("runtime rejection changed database: %v", err)
			}
		})
	}
}

func TestNodeStateLeavesLegacyRootFilesUntouched(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "node")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := map[string][]byte{
		nodeDatabaseFile:    []byte("old database"),
		nodeInitializedFile: []byte("old marker"),
		"frps-legacy.toml":  []byte("old FRPS config"),
		"snapshot-legacy":   []byte("old transfer"),
	}
	for name, contents := range legacy {
		if err := os.WriteFile(filepath.Join(directory, name), contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	state, err := OpenState(directory)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := state.Fingerprint()
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	state, err = OpenState(directory)
	if err != nil {
		t.Fatal(err)
	}
	if state.Fingerprint() != fingerprint {
		t.Fatal("v1 identity changed across restart")
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	for name, want := range legacy {
		got, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("legacy %s changed: %q, %v", name, got, err)
		}
	}
}

func TestNodeHealthContainsNoIdentityOrState(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "node")
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output bytes.Buffer
	finished := make(chan error, 1)
	go func() {
		finished <- Run(ctx, Config{ManagementBindAddress: "127.0.0.1", ManagementPort: port, DataDir: directory}, &output)
	}()
	client := &http.Client{Timeout: time.Second}
	var body []byte
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", port))
		if err == nil {
			body, err = io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil || response.StatusCode != http.StatusOK {
				t.Fatalf("health = (%s, %v)", body, err)
			}
			break
		}
	}
	if len(body) == 0 || !bytes.Contains(body, []byte(`"protocolVersion":1`)) || bytes.Contains(body, []byte("SHA256")) || bytes.Contains(body, []byte("controller")) {
		t.Fatalf("invalid public health body %q", body)
	}
	if !strings.Contains(output.String(), "Node fingerprint: SHA256:") {
		t.Fatalf("startup omitted fingerprint: %q", output.String())
	}
	cancel()
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}
