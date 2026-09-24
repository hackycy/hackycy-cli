package node

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

func Run(ctx context.Context, config Config, output io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	state, err := OpenState(config.DataDir)
	if err != nil {
		return err
	}
	defer state.Close()
	listener, err := net.Listen("tcp", net.JoinHostPort(config.ManagementBindAddress, strconv.Itoa(config.ManagementPort)))
	if err != nil {
		return fmt.Errorf("listen for Node management: %w", err)
	}
	defer listener.Close()
	handler := newManagementHandler(state)
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	if output != nil {
		_, _ = fmt.Fprintf(output, "Node fingerprint: %s\nManagement listener: http://%s\n", state.Fingerprint(), listener.Addr())
	}
	stopped := make(chan error, 1)
	go func() { stopped <- server.Serve(listener) }()
	select {
	case err := <-stopped:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}
