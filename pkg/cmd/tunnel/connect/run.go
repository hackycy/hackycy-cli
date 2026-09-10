package connect

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/hackycy/hackycy-cli/internal/logging"
	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
)

// ConnectionStore is the local encrypted catalog and client-identity owner
// required by tunnel connect.
type ConnectionStore interface {
	ClientConnectionReader
	ClientInstanceIdentity
	RememberTunnelConnection(*url.URL, string, time.Time) error
}

// Options contains the parsed Tunnel connect request and leaf-owned dependencies.
type Options struct {
	Context     context.Context
	Input       ClientOptionInput
	ConfigStore func() (ConnectionStore, error)
	Environment ClientEnvironment
	Terminal    *terminalexperience.Runtime
	Logger      logging.Logger
	YCYVersion  string
	Now         func() time.Time
}

func runConnect(options *Options) error {
	if options == nil {
		return errors.New("Tunnel connect options are required")
	}
	if options.ConfigStore == nil || options.Environment == nil || options.Terminal == nil {
		return errors.New("Tunnel connect options are incomplete")
	}
	store, err := options.ConfigStore()
	if err != nil {
		return err
	}
	if store == nil {
		return errors.New("Tunnel connection catalog is required")
	}
	resolved, err := ResolveClientConfig(options.Context, options.Input, ClientResolutionOptions{
		Reader:           store,
		Environment:      options.Environment,
		DefaultServer:    DefaultTunnelServer,
		SelectConnection: terminalTunnelConnectionSelectorFor(options.Terminal),
	})
	if err != nil {
		options.Logger.Error("Could not resolve tunnel client configuration", map[string]any{"error": err.Error()})
		return err
	}
	if resolved == nil {
		return nil
	}
	runOptions := ClientRunOptions{
		InstanceIdentity: store,
		Logger:           options.Logger,
		YCYVersion:       options.YCYVersion,
	}
	if resolved.RememberOnAuthentication {
		config := resolved.Config
		now := options.Now
		if now == nil {
			now = time.Now
		}
		runOptions.OnAuthenticated = func() error {
			return store.RememberTunnelConnection(config.Server, config.Token, now())
		}
	}
	return RunClient(options.Context, resolved.Config, runOptions)
}
