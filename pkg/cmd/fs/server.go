package fs

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"sync"
)

// ServerOptions selects the listener and protocol configuration for one FS
// runtime. Release is called exactly once after serving stops.
type ServerOptions struct {
	BindingAddress string
	Port           int
	ReadOnly       ReadOnlyServerOptions
	Release        func() error
	BeforeRelease  func(string, error)
}

// RunningServer owns the FS listener and the resource-release boundary for
// one already-constructed runtime.
type RunningServer struct {
	listener      net.Listener
	httpServer    *http.Server
	release       func() error
	beforeRelease func(string, error)
	done          chan struct{}

	mu         sync.RWMutex
	serveErr   error
	closeErr   error
	releaseErr error
	close      sync.Once
	released   sync.Once
}

// StartServer binds a composed FS handler and begins serving immediately.
func StartServer(workspace *Workspace, options ServerOptions) (*RunningServer, error) {
	handler, err := NewServerHandler(workspace, options.ReadOnly)
	if err != nil {
		if options.Release != nil {
			return nil, errors.Join(err, options.Release())
		}
		return nil, err
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(options.BindingAddress, strconv.Itoa(options.Port)))
	if err != nil {
		if options.Release != nil {
			return nil, errors.Join(err, options.Release())
		}
		return nil, err
	}
	server := &RunningServer{
		listener:      listener,
		release:       options.Release,
		beforeRelease: options.BeforeRelease,
		done:          make(chan struct{}),
	}
	server.httpServer = &http.Server{Handler: handler}
	go server.serve()
	return server, nil
}

// URL reports the concrete listener address, including a kernel-assigned
// port when the caller selected port zero.
func (server *RunningServer) URL() string {
	return "http://" + server.listener.Addr().String()
}

// Port reports the selected TCP port.
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

// Close force-closes HTTP connections, waits for serving to finish, and then
// releases the runtime's owned resources.
func (server *RunningServer) Close() error {
	server.close.Do(func() {
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

// Wait blocks until the listener exits and reports unexpected serve or
// resource-release failures.
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
	server.reportReleaseFailure(err)
	server.releaseResources()
	close(server.done)
}

func (server *RunningServer) releaseResources() {
	server.released.Do(func() {
		if server.release == nil {
			return
		}
		server.mu.Lock()
		server.releaseErr = server.release()
		server.mu.Unlock()
	})
}

func (server *RunningServer) reportReleaseFailure(serveErr error) {
	if server == nil || server.beforeRelease == nil {
		return
	}
	if serveErr != nil {
		server.beforeRelease("serve", serveErr)
		return
	}
	server.mu.RLock()
	closeErr := server.closeErr
	server.mu.RUnlock()
	if errors.Is(closeErr, http.ErrServerClosed) {
		closeErr = nil
	}
	if closeErr != nil {
		server.beforeRelease("close", closeErr)
	}
}
