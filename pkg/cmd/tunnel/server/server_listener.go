package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"sync"
)

// RunningServer owns the Tunnel control HTTP listener and releases its
// composed runtime after serving stops.
type RunningServer struct {
	listener   net.Listener
	httpServer *http.Server
	runtime    *ServerRuntime
	done       chan struct{}
	shutdown   context.Context
	cancel     context.CancelFunc

	mu         sync.RWMutex
	serveErr   error
	closeErr   error
	releaseErr error
	close      sync.Once
	released   sync.Once
	startFRPS  sync.Once
}

// Start binds the configured control HTTP listener. FRPS activation remains a
// separate lifecycle action, so the control plane is immediately available
// while managed FRPS is stopped.
func (runtime *ServerRuntime) Start() (*RunningServer, error) {
	return runtime.start(true)
}

// start binds the control plane and optionally starts managed FRPS. Keeping
// the two steps separate lets the command lifecycle report that the control
// plane is usable before independent data-plane preparation begins.
func (runtime *ServerRuntime) start(startFRPS bool) (*RunningServer, error) {
	if runtime == nil {
		return nil, errors.New("Tunnel server runtime is required")
	}
	if runtime.handler == nil || runtime.frps == nil {
		return nil, errors.Join(errors.New("Tunnel server runtime is incomplete"), runtime.Close())
	}
	settings := runtime.frps.State().Settings
	listener, err := net.Listen("tcp", net.JoinHostPort(settings.Address, strconv.Itoa(settings.ControlPort)))
	if err != nil {
		return nil, errors.Join(err, runtime.Close())
	}
	shutdown, cancel := context.WithCancel(context.Background())
	server := &RunningServer{
		listener: listener,
		runtime:  runtime,
		done:     make(chan struct{}),
		shutdown: shutdown,
		cancel:   cancel,
	}
	server.httpServer = &http.Server{Handler: runtime.handler}
	go server.serve()
	if startFRPS {
		server.startManagedFRPS()
	}
	return server, nil
}

// URL reports the concrete listener address, including a kernel-assigned port.
func (server *RunningServer) URL() string {
	return "http://" + server.listener.Addr().String()
}

// Port reports the selected control TCP port.
func (server *RunningServer) Port() int {
	if address, ok := server.listener.Addr().(*net.TCPAddr); ok {
		return address.Port
	}
	_, rawPort, err := net.SplitHostPort(server.listener.Addr().String())
	if err != nil {
		return 0
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil {
		return 0
	}
	return port
}

// Close force-closes control HTTP connections, waits for serving to end, and
// then releases the composed runtime exactly once.
func (server *RunningServer) Close() error {
	server.close.Do(func() {
		server.cancel()
		server.mu.Lock()
		server.closeErr = server.httpServer.Close()
		server.mu.Unlock()
	})
	<-server.done
	server.releaseResources()
	server.mu.RLock()
	defer server.mu.RUnlock()
	closeErr := server.closeErr
	if errors.Is(closeErr, http.ErrServerClosed) {
		closeErr = nil
	}
	return errors.Join(closeErr, server.releaseErr)
}

// Wait blocks until the listener exits and reports unexpected serving or
// runtime-release failures.
func (server *RunningServer) Wait() error {
	<-server.done
	server.releaseResources()
	server.mu.RLock()
	defer server.mu.RUnlock()
	return errors.Join(server.serveErr, server.releaseErr)
}

func (server *RunningServer) serve() {
	err := server.httpServer.Serve(server.listener)
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	server.mu.Lock()
	server.serveErr = err
	server.mu.Unlock()
	server.cancel()
	close(server.done)
	server.releaseResources()
}

func (server *RunningServer) startManagedFRPS() {
	server.startFRPS.Do(func() {
		go func() {
			_ = server.runtime.frps.Start(server.shutdown)
		}()
	})
}

func (server *RunningServer) releaseResources() {
	server.released.Do(func() {
		if server.runtime == nil {
			return
		}
		server.mu.Lock()
		server.releaseErr = server.runtime.Close()
		server.mu.Unlock()
	})
}
