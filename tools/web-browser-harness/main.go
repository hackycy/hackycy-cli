package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	webassets "github.com/hackycy/hackycy-cli/web"
)

type runningServer struct {
	application string
	listener    net.Listener
	server      *http.Server
}

func main() {
	servers := make([]runningServer, 0, 3)
	for _, application := range []string{"diff", "fs", "tunnel-server"} {
		handler, err := webassets.NewReadinessHandler(application, webassets.ReadinessHandlerOptions{})
		if err != nil {
			fail(err)
		}
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			fail(err)
		}
		server := &http.Server{Handler: handler}
		servers = append(servers, runningServer{application: application, listener: listener, server: server})
		go func() {
			if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
				fail(err)
			}
		}()
	}

	urls := make(map[string]string, len(servers))
	for _, server := range servers {
		urls[server.application] = fmt.Sprintf("http://%s", server.listener.Addr())
	}
	if err := json.NewEncoder(os.Stdout).Encode(urls); err != nil {
		fail(err)
	}

	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	<-signalContext.Done()
	shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, server := range servers {
		_ = server.server.Shutdown(shutdownContext)
	}
}

func fail(err error) {
	_, _ = fmt.Fprintf(os.Stderr, "web browser harness: %v\n", err)
	os.Exit(1)
}
