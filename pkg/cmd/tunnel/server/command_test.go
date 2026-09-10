package server

import (
	"bytes"
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/terminal"
	commandfactory "github.com/hackycy/hackycy-cli/pkg/cmd/factory"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
)

func TestNewCmdServerResolvesCLIEnvironmentAndInvokesLeaf(t *testing.T) {
	configuredDataDirectory := filepath.Join(t.TempDir(), "configured")
	environmentDataDirectory := filepath.Join(t.TempDir(), "environment")
	environment := map[string]string{
		"YCY_TUNNEL_ADDRESS":            "0.0.0.0",
		"YCY_TUNNEL_CONTROL_PORT":       "7500",
		"YCY_TUNNEL_FRP_PORT":           "7000",
		"YCY_TUNNEL_HTTP_PORT":          "8080",
		"YCY_TUNNEL_PORT_RANGE":         "20000-20100",
		"YCY_TUNNEL_ADVERTISE_FRP_ADDR": "environment.example.test:7555",
		"YCY_TUNNEL_DATA_DIR":           environmentDataDirectory,
		"YCY_TUNNEL_SESSION_IDLE_DAYS":  "7",
		"YCY_TUNNEL_ADMIN_USER":         "ops-admin",
		"YCY_TUNNEL_ADMIN_PASSWORD":     "environment-password",
		"YCY_TUNNEL_FRP_TOKEN":          "environment-token",
	}
	var options []*Options
	for _, arguments := range [][]string{
		{
			"--address", "127.0.0.1", "--control-port", "7501", "--frp-port", "7001", "--http-port", "8081",
			"--port-range", "21000-21100", "--advertise-frp-addr", "[2001:db8::1]:7443",
			"--data-dir", configuredDataDirectory, "--session-idle-days", "8",
		},
		nil,
	} {
		command := NewCmdServer(newServerTestFactory(environment), func(option *Options) error {
			options = append(options, option)
			return nil
		})
		command.SetArgs(arguments)
		if err := command.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("%q ExecuteContext() error = %v", arguments, err)
		}
	}

	want := []ServerConfig{
		{
			Settings: ServerHTTPServerSettings{
				Address:          "127.0.0.1",
				ControlPort:      7501,
				FRPPort:          7001,
				HTTPPort:         8081,
				PortRange:        ServerHTTPPortRange{Start: 21000, End: 21100},
				AdvertiseFRPAddr: &ServerHTTPFRPAddress{Host: "2001:db8::1", Port: 7443},
				DataDir:          configuredDataDirectory,
				AdminUser:        "ops-admin",
			},
			AdminPassword:       "environment-password",
			SessionIdleLifetime: 8 * 24 * time.Hour,
			FRPToken:            "environment-token",
		},
		{
			Settings: ServerHTTPServerSettings{
				Address:          "0.0.0.0",
				ControlPort:      7500,
				FRPPort:          7000,
				HTTPPort:         8080,
				PortRange:        ServerHTTPPortRange{Start: 20000, End: 20100},
				AdvertiseFRPAddr: &ServerHTTPFRPAddress{Host: "environment.example.test", Port: 7555},
				DataDir:          environmentDataDirectory,
				AdminUser:        "ops-admin",
			},
			AdminPassword:       "environment-password",
			SessionIdleLifetime: 7 * 24 * time.Hour,
			FRPToken:            "environment-token",
		},
	}
	if len(options) != len(want) {
		t.Fatalf("options = %#v", options)
	}
	for index := range want {
		if options[index] == nil || options[index].Context == nil || !reflect.DeepEqual(options[index].Config, want[index]) {
			t.Fatalf("option %d = %#v, want config %#v", index, options[index], want[index])
		}
	}
}

func TestNewCmdServerRejectsInvalidConfigurationBeforeInvokingLeaf(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		environment map[string]string
		arguments   []string
		message     string
	}{
		{name: "missing password", arguments: nil, message: "YCY_TUNNEL_ADMIN_PASSWORD"},
		{name: "invalid port", environment: map[string]string{"YCY_TUNNEL_ADMIN_PASSWORD": "environment-password"}, arguments: []string{"--control-port=0"}, message: "Control port must be an integer"},
		{name: "empty configured token", environment: map[string]string{"YCY_TUNNEL_ADMIN_PASSWORD": "environment-password", "YCY_TUNNEL_FRP_TOKEN": ""}, arguments: nil, message: "FRP Token must not be empty"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			calls := 0
			command := NewCmdServer(newServerTestFactory(testCase.environment), func(*Options) error {
				calls++
				return nil
			})
			command.SetArgs(testCase.arguments)
			err := command.ExecuteContext(context.Background())
			if err == nil || calls != 0 || !strings.Contains(err.Error(), testCase.message) {
				t.Fatalf("execution = (%v, calls=%d), want %q", err, calls, testCase.message)
			}
		})
	}
}

func TestNewCmdServerPreservesLeafHelpWithoutRunning(t *testing.T) {
	output := &bytes.Buffer{}
	calls := 0
	command := NewCmdServer(newServerTestFactory(nil), func(*Options) error {
		calls++
		return nil
	})
	command.SetOut(output)
	command.SetArgs([]string{"--help"})
	if err := command.ExecuteContext(context.Background()); err != nil || calls != 0 || !strings.Contains(output.String(), "--control-port") || !strings.Contains(output.String(), "--session-idle-days") || strings.Contains(output.String(), "--log-level") {
		t.Fatalf("help execution = (%v, calls=%d, output=%q)", err, calls, output.String())
	}
}

func newServerTestFactory(environment map[string]string) *cmdutil.Factory {
	return commandfactory.New(commandfactory.Options{
		Version: "0.0.0-dev",
		IOStreams: cmdutil.IOStreams{
			Out:    &bytes.Buffer{},
			ErrOut: &bytes.Buffer{},
		},
		Environment: func(key string) string {
			return environment[key]
		},
		EnvironmentLookup: func(key string) (string, bool) {
			value, ok := environment[key]
			return value, ok
		},
		Capabilities: terminal.Capabilities{Interaction: terminal.Automation},
	})
}
