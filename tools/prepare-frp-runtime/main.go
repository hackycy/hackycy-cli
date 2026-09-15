package main

import (
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

// The Docker build keeps one pair per Linux architecture so Buildx can select
// the matching payload through TARGETARCH without downloading during docker build.
var linuxTargets = []tunnelruntime.WireTarget{
	{Platform: tunnelruntime.WirePlatformLinux, Architecture: tunnelruntime.WireArchitectureX64},
	{Platform: tunnelruntime.WirePlatformLinux, Architecture: tunnelruntime.WireArchitectureARM64},
}

func main() {
	output := flag.String("output", ".tmp/docker", "directory for Docker build inputs")
	flag.Parse()
	if flag.NArg() != 0 {
		fail(fmt.Errorf("pass only --output <directory>"))
	}
	if err := prepare(context.Background(), *output); err != nil {
		fail(err)
	}
}

func prepare(ctx context.Context, output string) error {
	return prepareWithClient(ctx, output, http.DefaultClient)
}

func prepareWithClient(ctx context.Context, output string, client *http.Client) error {
	if output == "" {
		return fmt.Errorf("output directory is required")
	}
	for _, target := range linuxTargets {
		artifact, err := tunnelruntime.ResolveFRPArtifact(target)
		if err != nil {
			return fmt.Errorf("resolve FRP %s/%s: %w", target.Platform, target.Architecture, err)
		}
		if err := prepareArtifact(ctx, filepath.Join(output, "frp"), artifact, client); err != nil {
			return err
		}
	}
	return nil
}

func prepareArtifact(ctx context.Context, output string, artifact tunnelruntime.FRPArtifact, client *http.Client) error {
	architecture := string(artifact.Target.Architecture)
	directory := filepath.Join(output, "linux-"+architecture)
	paths, err := tunnelruntime.PrepareFRPRuntimeAtWithClient(ctx, directory, artifact, client)
	if err != nil {
		return fmt.Errorf("prepare FRP linux/%s: %w", architecture, err)
	}
	for name, path := range map[string]string{"frpc": paths.FRPC, "frps": paths.FRPS} {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return fmt.Errorf("verify prepared %s: %w", name, statErr)
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("prepared %s is not a non-empty regular file: %s", name, path)
		}
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("read prepared %s: %w", name, readErr)
		}
		actual := fmt.Sprintf("%x", sha256.Sum256(contents))
		want := artifact.Description.FRPCSHA256
		if name == "frps" {
			want = artifact.FRPSSHA256
		}
		if actual != want {
			return fmt.Errorf("prepared %s SHA-256 = %s, want %s", name, actual, want)
		}
	}
	return nil
}

func fail(err error) {
	_, _ = fmt.Fprintf(os.Stderr, "prepare-frp-runtime: %v\n", err)
	os.Exit(1)
}
