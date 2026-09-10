package list

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
)

func TestModuleReturnsTheSafeForkList(t *testing.T) {
	instances := []appconfig.ForkInstance{{
		Name:         "work",
		Host:         "gitlab.example",
		Scheme:       "https",
		Type:         "gitlab",
		TokenPreview: "MDEy***",
	}}
	module, err := New(Dependencies{
		Reader: fakeReader{instances: instances},
	})
	if err != nil {
		t.Fatalf("New() returned an error: %v", err)
	}

	result, err := module.Run(context.Background(), Input{})
	if err != nil {
		t.Fatalf("Run() returned an error: %v", err)
	}
	if want := []Instance{{Name: "work", Host: "gitlab.example", Scheme: "https", Type: "gitlab", TokenPreview: "MDEy***"}}; !reflect.DeepEqual(result.Instances, want) {
		t.Fatalf("Run() instances = %#v, want %#v", result.Instances, want)
	}
}

func TestModuleReturnsReaderFailure(t *testing.T) {
	readFailure := errors.New("read configuration")
	module, err := New(Dependencies{Reader: fakeReader{err: readFailure}})
	if err != nil {
		t.Fatalf("New() returned an error: %v", err)
	}
	if _, err := module.Run(context.Background(), Input{}); !errors.Is(err, readFailure) {
		t.Fatalf("Run() error = %v, want %v", err, readFailure)
	}
}

func TestNewRequiresAReader(t *testing.T) {
	if _, err := New(Dependencies{}); err == nil {
		t.Fatal("New() accepted a nil Reader")
	}
}
