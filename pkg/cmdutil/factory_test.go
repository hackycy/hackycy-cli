package cmdutil

import (
	"io"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
	"github.com/hackycy/hackycy-cli/internal/gitprocess"
	"github.com/hackycy/hackycy-cli/internal/logging"
	"github.com/hackycy/hackycy-cli/internal/terminal"
)

func TestFactoryHasOnlyApprovedCapabilities(t *testing.T) {
	want := []struct {
		name   string
		typeOf reflect.Type
	}{
		{name: "Version", typeOf: reflect.TypeFor[string]()},
		{name: "IOStreams", typeOf: reflect.TypeFor[IOStreams]()},
		{name: "Terminal", typeOf: reflect.TypeFor[*terminal.Runtime]()},
		{name: "Logging", typeOf: reflect.TypeFor[*logging.Runtime]()},
		{name: "Environment", typeOf: reflect.TypeFor[func(string) string]()},
		{name: "EnvironmentLookup", typeOf: reflect.TypeFor[func(string) (string, bool)]()},
		{name: "WorkingDirectory", typeOf: reflect.TypeFor[func() (string, error)]()},
		{name: "HTTPClient", typeOf: reflect.TypeFor[*http.Client]()},
		{name: "Now", typeOf: reflect.TypeFor[func() time.Time]()},
		{name: "ConfigStore", typeOf: reflect.TypeFor[func() (*appconfig.Store, error)]()},
		{name: "GitRunner", typeOf: reflect.TypeFor[func() *gitprocess.Runner]()},
	}

	factoryType := reflect.TypeFor[Factory]()
	if factoryType.NumField() != len(want) {
		t.Fatalf("Factory fields = %d, want %d", factoryType.NumField(), len(want))
	}
	for index, expected := range want {
		field := factoryType.Field(index)
		if field.Name != expected.name || field.Type != expected.typeOf {
			t.Fatalf("Factory field %d = %s %s, want %s %s", index, field.Name, field.Type, expected.name, expected.typeOf)
		}
	}
}

func TestIOStreamsPreservesTheApprovedInheritedStreams(t *testing.T) {
	want := []struct {
		name   string
		typeOf reflect.Type
	}{
		{name: "In", typeOf: reflect.TypeFor[io.Reader]()},
		{name: "Out", typeOf: reflect.TypeFor[io.Writer]()},
		{name: "ErrOut", typeOf: reflect.TypeFor[io.Writer]()},
	}

	streamsType := reflect.TypeFor[IOStreams]()
	if streamsType.NumField() != len(want) {
		t.Fatalf("IOStreams fields = %d, want %d", streamsType.NumField(), len(want))
	}
	for index, expected := range want {
		field := streamsType.Field(index)
		if field.Name != expected.name || field.Type != expected.typeOf {
			t.Fatalf("IOStreams field %d = %s %s, want %s %s", index, field.Name, field.Type, expected.name, expected.typeOf)
		}
	}
}
