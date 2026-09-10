package zip

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestCollectArchiveFilesMatchesRootGlobsExcludesDotsAndDeduplicates(t *testing.T) {
	root := t.TempDir()
	writeZipFile(t, filepath.Join(root, "index.html"), "root")
	writeZipFile(t, filepath.Join(root, "assets", "app.js"), "app")
	writeZipFile(t, filepath.Join(root, "assets", "style.css"), "style")
	writeZipFile(t, filepath.Join(root, ".secret"), "secret")
	writeZipFile(t, filepath.Join(root, "nested", ".cache", "hidden.js"), "hidden")
	if err := os.Symlink(filepath.Join(root, "index.html"), filepath.Join(root, "linked.html")); err != nil {
		t.Fatalf("Symlink(): %v", err)
	}

	entries, err := CollectArchiveFiles(root, []string{"**/*.html", "assets/**/*", "**/*.html"})
	if err != nil {
		t.Fatalf("CollectArchiveFiles() error = %v", err)
	}
	want := []string{"index.html", "assets/app.js", "assets/style.css"}
	if got := archiveRelativePaths(entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("entries = %#v, want %#v", got, want)
	}
}

func TestBuildZipDataPreservesPathsBytesAndSelectedMetadataLoss(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "index.html")
	second := filepath.Join(root, "assets", "app.js")
	output := filepath.Join(root, "archive.zip")
	writeZipFile(t, first, "<main>first</main>")
	writeZipFile(t, second, "console.log('second')")
	writeZipFile(t, output, "old archive")
	oldTime := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	if err := os.Chmod(first, 0o711); err != nil {
		t.Fatalf("Chmod(): %v", err)
	}
	if err := os.Chtimes(first, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes(): %v", err)
	}

	entries, err := CollectArchiveFiles(root, []string{defaultGlobPattern})
	if err != nil {
		t.Fatalf("CollectArchiveFiles() error = %v", err)
	}
	data, included, err := BuildZipData(entries, output, "release/../bundle")
	if err != nil {
		t.Fatalf("BuildZipData() error = %v", err)
	}
	if included != 2 {
		t.Fatalf("included = %d, want 2", included)
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader() error = %v", err)
	}
	if len(reader.File) != 2 {
		t.Fatalf("archive entries = %d, want 2", len(reader.File))
	}
	wantContents := map[string]string{
		"release/../bundle/index.html":    "<main>first</main>",
		"release/../bundle/assets/app.js": "console.log('second')",
	}
	var archiveTime time.Time
	for _, file := range reader.File {
		contents, err := readZipFile(file)
		if err != nil {
			t.Fatalf("read %q: %v", file.Name, err)
		}
		if got, ok := wantContents[file.Name]; !ok || contents != got {
			t.Fatalf("entry %q contents = %q, want %#v", file.Name, contents, wantContents)
		}
		if archiveTime.IsZero() {
			archiveTime = file.Modified
		} else if !file.Modified.Equal(archiveTime) {
			t.Fatalf("entry times differ: %s and %s", archiveTime, file.Modified)
		}
		if file.Name == "release/../bundle/index.html" && file.Mode().Perm() == 0o711 {
			t.Fatalf("source mode unexpectedly preserved: %o", file.Mode().Perm())
		}
		if file.Name == "release/../bundle/index.html" && file.Modified.Equal(oldTime) {
			t.Fatalf("source modification time unexpectedly preserved: %s", file.Modified)
		}
	}
}

func TestBuildZipDataRejectsOnlyTheExistingOutput(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "archive.zip")
	writeZipFile(t, output, "old archive")
	data, included, err := BuildZipData([]ArchiveEntry{{Relative: "archive.zip", Absolute: output}}, output, "")
	if !errors.Is(err, errNoValidArchiveFiles) || err.Error() != "No valid files matched after filtering." {
		t.Fatalf("BuildZipData() error = %v", err)
	}
	if data != nil || included != 0 {
		t.Fatalf("data = %v, included = %d", data, included)
	}
}

func TestWriteZipFileOverwritesTheExistingDestination(t *testing.T) {
	output := filepath.Join(t.TempDir(), "archive.zip")
	writeZipFile(t, output, "old archive")
	want := []byte("new archive")
	if err := WriteZipFile(output, want); err != nil {
		t.Fatalf("WriteZipFile() error = %v", err)
	}
	got, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("written bytes = %q, want %q", got, want)
	}
}

func TestBuildZipDataReadsLargeInputInTheCompleteArchiveBuffer(t *testing.T) {
	root := t.TempDir()
	contents := bytes.Repeat([]byte("abcdefgh"), 256*1024)
	input := filepath.Join(root, "large.js")
	if err := os.WriteFile(input, contents, 0o600); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}
	data, included, err := BuildZipData([]ArchiveEntry{{Relative: "large.js", Absolute: input}}, filepath.Join(root, "archive.zip"), "")
	if err != nil {
		t.Fatalf("BuildZipData() error = %v", err)
	}
	if included != 1 {
		t.Fatalf("included = %d, want 1", included)
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader() error = %v", err)
	}
	got, err := readZipFile(reader.File[0])
	if err != nil {
		t.Fatalf("readZipFile() error = %v", err)
	}
	if !bytes.Equal([]byte(got), contents) {
		t.Fatalf("large input bytes changed")
	}
}

func archiveRelativePaths(entries []ArchiveEntry) []string {
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, entry.Relative)
	}
	return paths
}

func readZipFile(file *zip.File) (string, error) {
	reader, err := file.Open()
	if err != nil {
		return "", err
	}
	defer reader.Close()
	contents, err := io.ReadAll(reader)
	return string(contents), err
}
