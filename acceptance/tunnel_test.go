//go:build acceptance

package acceptance

import (
	"strings"
	"testing"
)

func TestTunnelServerStandaloneBinaryPreservesCLIValidation(t *testing.T) {
	binary := buildDiffStandaloneBinary(t)
	environment := environmentWith(map[string]string{
		"HOME":                         t.TempDir(),
		"USERPROFILE":                  "",
		"YCY_TUNNEL_ADMIN_USER":        "admin",
		"YCY_TUNNEL_ADMIN_PASSWORD":    "standalone-password",
		"YCY_TUNNEL_FRP_TOKEN":         "standalone-token",
		"YCY_TUNNEL_DOCKER":            "",
		"YCY_TUNNEL_ADDRESS":           "127.0.0.1",
		"YCY_TUNNEL_CONTROL_PORT":      "7500",
		"YCY_TUNNEL_FRP_PORT":          "7000",
		"YCY_TUNNEL_HTTP_PORT":         "8080",
		"YCY_TUNNEL_PORT_RANGE":        "20000-20100",
		"YCY_TUNNEL_DATA_DIR":          t.TempDir(),
		"YCY_TUNNEL_SESSION_IDLE_DAYS": "7",
	})

	help, err := runDiffStandalone(binary, t.TempDir(), environment, "tunnel", "server", "--help")
	if err != nil {
		t.Fatalf("tunnel server --help: %v\n%s", err, help)
	}
	for _, expected := range []string{
		"Run the Tunnel Control Plane and supervised frps process",
		"--address", "--control-port", "--frp-port", "--http-port", "--port-range",
		"--advertise-frp-addr", "--data-dir", "--session-idle-days",
		"Flags:", "--log-level",
	} {
		if !strings.Contains(string(help), expected) {
			t.Fatalf("tunnel server help omitted %q:\n%s", expected, help)
		}
	}

	for _, testCase := range []struct {
		arguments []string
		message   string
	}{
		{arguments: []string{"tunnel", "server", "--control-port", "0"}, message: "Control port must be an integer from 1 through 65535"},
		{arguments: []string{"tunnel", "server", "--control-port", "7000", "--frp-port", "7000"}, message: "Control, FRP bind, and FRP HTTP listener ports must be distinct"},
		{arguments: []string{"tunnel", "server", "--port-range", "7000-7010"}, message: "Server Port Pool must not include listener port 7000"},
		{arguments: []string{"tunnel", "server", "--advertise-frp-addr", "https://tunnels.example.test:7000"}, message: "Advertised FRP address must be host:port or [IPv6]:port"},
		{arguments: []string{"tunnel", "connect", "--server", "", "--token", "client-token"}, message: "Control plane must not be empty"},
	} {
		output, runErr := runDiffStandalone(binary, t.TempDir(), environment, testCase.arguments...)
		if exitCode(runErr) != 1 || !strings.Contains(string(output), testCase.message) {
			t.Fatalf("arguments %q = (%v, %q), want %q", testCase.arguments, runErr, output, testCase.message)
		}
	}

	connectHelp, err := runDiffStandalone(binary, t.TempDir(), environment, "tunnel", "connect", "--help")
	if err != nil {
		t.Fatalf("tunnel connect --help: %v\n%s", err, connectHelp)
	}
	for _, expected := range []string{
		"Connect a native trusted client to a Tunnel Control Plane",
		"--server", "--token", "Flags:", "--log-level",
	} {
		if !strings.Contains(string(connectHelp), expected) {
			t.Fatalf("tunnel connect help omitted %q:\n%s", expected, connectHelp)
		}
	}

	withoutPassword := environmentWith(map[string]string{
		"HOME":                         t.TempDir(),
		"USERPROFILE":                  "",
		"YCY_TUNNEL_ADMIN_PASSWORD":    "",
		"YCY_TUNNEL_DOCKER":            "",
		"YCY_TUNNEL_ADDRESS":           "127.0.0.1",
		"YCY_TUNNEL_CONTROL_PORT":      "7500",
		"YCY_TUNNEL_FRP_PORT":          "7000",
		"YCY_TUNNEL_HTTP_PORT":         "8080",
		"YCY_TUNNEL_PORT_RANGE":        "20000-20100",
		"YCY_TUNNEL_DATA_DIR":          t.TempDir(),
		"YCY_TUNNEL_SESSION_IDLE_DAYS": "7",
	})
	output, runErr := runDiffStandalone(binary, t.TempDir(), withoutPassword, "tunnel", "server")
	if exitCode(runErr) != 1 || !strings.Contains(string(output), "YCY_TUNNEL_ADMIN_PASSWORD must contain 5-256 characters") {
		t.Fatalf("missing administrator password = (%v, %q)", runErr, output)
	}

}
