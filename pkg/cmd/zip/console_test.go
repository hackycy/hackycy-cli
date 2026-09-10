package zip

import (
	"reflect"
	"testing"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestZIPConsoleDescriptorProvidesSafeArchiveContext(t *testing.T) {
	want := terminalexperience.ConsoleDescriptor{
		Command: "YCY / zip",
		Target:  "Plan and publish a bounded archive",
		Status:  "READY",
		Metadata: []terminalexperience.ConsoleMetadata{
			{Label: "summary", Value: "Zip Directory"},
			{Label: "directory", Value: "project"},
			{Label: "with-dir", Value: "enabled"},
			{Label: "reveal", Value: "disabled"},
		},
		FormCatalog: []terminalexperience.ConsoleFormStep{
			{ID: zipPackageFormID, Name: "Workspace package", Detail: "select when multiple packages are found"},
			{ID: zipSourceFormID, Name: "Source directory", Detail: "choose archive source"},
			{ID: zipPatternsFormID, Name: "File patterns", Detail: "select files to include"},
			{ID: zipOutputFormID, Name: "Output name", Detail: "name the archive"},
		},
	}
	if got := terminalZipConsoleDescriptor(&Options{Directory: "/private/project", WithDir: "bundle"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("Console descriptor = %#v, want %#v", got, want)
	}
	unsafe := terminalZipConsoleDescriptor(&Options{Directory: "bad\n/absolute/secret", WithDir: "../unsafe", Open: true})
	for _, field := range []string{unsafe.Command, unsafe.Target, unsafe.Status} {
		if terminaltest.ContainsTerminalControl([]byte(field)) {
			t.Fatalf("descriptor field contains terminal control: %q", field)
		}
	}
	for _, metadata := range unsafe.Metadata {
		if terminaltest.ContainsTerminalControl([]byte(metadata.Label)) || terminaltest.ContainsTerminalControl([]byte(metadata.Value)) {
			t.Fatalf("descriptor metadata contains terminal control: %#v", metadata)
		}
	}
	if got := unsafe.Metadata[1].Value; got == "bad\n/absolute/secret" || got == "/absolute/secret" {
		t.Fatalf("directory projection exposed unsafe path: %q", got)
	}
}
