package list

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
)

func TestModuleReturnsTheSafeCMList(t *testing.T) {
	module, err := New(Dependencies{
		Reader: fakeReader{profiles: appconfig.CMProfileList{
			DefaultProfile: "personal",
			Profiles: []appconfig.CMProfile{
				{Name: "work", Model: "gpt-4.1-mini", BaseURL: "https://work.example/v1"},
				{Name: "personal", Model: "deepseek-chat", BaseURL: "https://personal.example/v1"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("New() returned an error: %v", err)
	}

	result, err := module.Run(context.Background(), Input{})
	if err != nil {
		t.Fatalf("Run() returned an error: %v", err)
	}
	if want := []Profile{
		{Name: "work", Model: "gpt-4.1-mini", BaseURL: "https://work.example/v1"},
		{Name: "personal", Model: "deepseek-chat", BaseURL: "https://personal.example/v1", Default: true},
	}; !reflect.DeepEqual(result.Profiles, want) {
		t.Fatalf("Run() profiles = %#v, want %#v", result.Profiles, want)
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
