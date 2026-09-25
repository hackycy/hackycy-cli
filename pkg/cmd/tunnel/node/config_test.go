package node

import (
	"path/filepath"
	"testing"
)

func TestResolveConfigPrecedenceAndValidation(t *testing.T) {
	environment := func(name string) (string, bool) {
		values := map[string]string{
			"YCY_TUNNEL_NODE_MANAGEMENT_BIND_ADDRESS": "127.0.0.1",
			"YCY_TUNNEL_NODE_MANAGEMENT_PORT":         "7601",
			"YCY_TUNNEL_NODE_DATA_DIR":                t.TempDir(),
		}
		value, ok := values[name]
		return value, ok
	}
	config, err := resolveConfig(Input{}, environment, func() (string, error) { return "unused", nil })
	if err != nil || config.ManagementBindAddress != "127.0.0.1" || config.ManagementPort != 7601 {
		t.Fatalf("environment config = (%+v, %v)", config, err)
	}
	bind, port, directory := "0.0.0.0", "7602", "relative-node"
	config, err = resolveConfig(Input{&bind, &port, &directory}, environment, nil)
	if err != nil || config.ManagementBindAddress != bind || config.ManagementPort != 7602 || !filepath.IsAbs(config.DataDir) || filepath.Base(config.DataDir) != directory {
		t.Fatalf("flag config = (%+v, %v)", config, err)
	}
	for _, input := range []Input{{ManagementBindAddress: stringPtr("")}, {ManagementBindAddress: stringPtr("example.com")}, {ManagementPort: stringPtr("0")}, {ManagementPort: stringPtr("65536")}, {ManagementPort: stringPtr("abc")}, {DataDir: stringPtr("")}} {
		if _, err := resolveConfig(input, environment, nil); err == nil {
			t.Fatalf("accepted invalid input %+v", input)
		}
	}
	defaultConfig, err := resolveConfig(Input{}, func(string) (string, bool) { return "", false }, func() (string, error) { return t.TempDir(), nil })
	if err != nil || defaultConfig.ManagementPort != 7600 || defaultConfig.ManagementBindAddress != "0.0.0.0" || !filepath.IsAbs(defaultConfig.DataDir) {
		t.Fatalf("default config = (%+v, %v)", defaultConfig, err)
	}
}

func stringPtr(value string) *string { return &value }
