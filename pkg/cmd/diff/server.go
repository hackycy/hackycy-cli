package diff

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"sync"
)

// RunningServer owns one listening Diff HTTP server and its initial Refresh.
type RunningServer struct {
	listener   net.Listener
	httpServer *http.Server
	refresh    *refreshCoordinator
	initialRun *RefreshRun
	done       chan struct{}

	mu       sync.RWMutex
	serveErr error
	closeErr error
	close    sync.Once
}

// StartServer binds the selected address, begins serving, and queues the first
// asynchronous Workspace Refresh before returning.
func StartServer(workspace *Workspace, bindingAddress string, port int) (*RunningServer, error) {
	return startServerWithLifecycle(workspace, bindingAddress, port, nil)
}

func startServerWithLifecycle(workspace *Workspace, bindingAddress string, port int, lifecycle *diffLifecycle) (*RunningServer, error) {
	handler, err := newServerHandlerWithLifecycle(workspace, bindingAddress, lifecycle)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(bindingAddress, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	running := &RunningServer{
		listener: listener,
		refresh:  handler.protocols.refresh,
		done:     make(chan struct{}),
	}
	running.httpServer = &http.Server{Handler: handler}
	go running.serve()
	if err := running.refresh.StartInitial(); err != nil {
		_ = running.Close()
		return nil, err
	}
	running.refresh.mu.Lock()
	running.initialRun = running.refresh.lastStarted
	running.refresh.mu.Unlock()
	return running, nil
}

// URL reports the listener's concrete HTTP URL, including a kernel-assigned port.
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

// Close cancels an active Refresh, force-closes client connections, and waits
// until the serving goroutine has stopped.
func (server *RunningServer) Close() error {
	server.close.Do(func() {
		server.refresh.StopAndCancel()
		server.mu.Lock()
		server.closeErr = server.httpServer.Close()
		server.mu.Unlock()
		server.refresh.CancelAndWait()
	})
	<-server.done
	server.mu.RLock()
	defer server.mu.RUnlock()
	if errors.Is(server.closeErr, http.ErrServerClosed) {
		return nil
	}
	return server.closeErr
}

// Wait blocks until the listener exits and reports an unexpected serve error.
func (server *RunningServer) Wait() error {
	<-server.done
	server.mu.RLock()
	defer server.mu.RUnlock()
	return server.serveErr
}

func (server *RunningServer) serve() {
	err := server.httpServer.Serve(server.listener)
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	server.mu.Lock()
	server.serveErr = err
	server.mu.Unlock()
	close(server.done)
}
