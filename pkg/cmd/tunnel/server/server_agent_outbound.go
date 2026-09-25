package server

import (
	"fmt"
	"sync"
	"time"

	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

const serverAgentWebSocketWriteTimeout = 10 * time.Second

// serverAgentOutbound is the sole data-frame writer for one agent socket.
// Desired snapshots are replaceable; terminal frames wait until they are sent.
type serverAgentOutbound struct {
	socket    serverAgentFrameWriter
	queue     chan serverAgentOutboundItem
	priority  chan serverAgentOutboundItem
	done      chan struct{}
	closeOnce sync.Once
	failOnce  sync.Once
	onFailure func(error)
}

type serverAgentFrameWriter interface {
	SetWriteDeadline(time.Time) error
	WriteJSON(any) error
}

type serverAgentOutboundItem struct {
	value  any
	result chan error
}

func newServerAgentOutbound(socket serverAgentFrameWriter, onFailure func(error)) *serverAgentOutbound {
	outbound := &serverAgentOutbound{
		socket: socket, queue: make(chan serverAgentOutboundItem, 1), priority: make(chan serverAgentOutboundItem, 1), done: make(chan struct{}), onFailure: onFailure,
	}
	go outbound.run()
	return outbound
}

func (outbound *serverAgentOutbound) WriteInitial(value any) error {
	if outbound == nil || outbound.socket == nil {
		return fmt.Errorf("Tunnel server agent writer is unavailable")
	}
	if err := outbound.socket.SetWriteDeadline(time.Now().Add(serverAgentWebSocketWriteTimeout)); err != nil {
		return err
	}
	err := outbound.socket.WriteJSON(value)
	_ = outbound.socket.SetWriteDeadline(time.Time{})
	return err
}

func (outbound *serverAgentOutbound) Write(value any) error {
	if outbound == nil {
		return fmt.Errorf("Tunnel server agent writer is unavailable")
	}
	select {
	case <-outbound.done:
		return fmt.Errorf("Tunnel server agent writer is closed")
	default:
	}
	if _, replaceable := value.(tunnelruntime.DesiredState); replaceable {
		item := serverAgentOutboundItem{value: value}
		select {
		case outbound.queue <- item:
		default:
			select {
			case previous := <-outbound.queue:
				if newerServerAgentDesired(previous.value, value) {
					item = previous
				}
			default:
			}
			select {
			case outbound.queue <- item:
			case <-outbound.done:
				return fmt.Errorf("Tunnel server agent writer is closed")
			}
		}
		return nil
	} else {
		item := serverAgentOutboundItem{value: value, result: make(chan error, 1)}
		target := outbound.queue
		if _, terminal := value.(tunnelruntime.Revoke); terminal {
			target = outbound.priority
		}
		select {
		case target <- item:
		case <-outbound.done:
			return fmt.Errorf("Tunnel server agent writer is closed")
		}
		select {
		case err := <-item.result:
			return err
		case <-outbound.done:
			return fmt.Errorf("Tunnel server agent writer is closed")
		}
	}
}

func newerServerAgentDesired(first, second any) bool {
	left, leftOK := first.(tunnelruntime.DesiredState)
	right, rightOK := second.(tunnelruntime.DesiredState)
	if !leftOK || !rightOK {
		return false
	}
	leftRuntime, rightRuntime := left.Runtime, right.Runtime
	if leftRuntime.Revision != rightRuntime.Revision {
		return leftRuntime.Revision > rightRuntime.Revision
	}
	if leftRuntime.NodeID != rightRuntime.NodeID {
		return leftRuntime.NodeID > rightRuntime.NodeID
	}
	if leftRuntime.Digest != rightRuntime.Digest {
		return leftRuntime.Digest > rightRuntime.Digest
	}
	return left.DesiredRestartGeneration >= right.DesiredRestartGeneration
}

func (outbound *serverAgentOutbound) run() {
	for {
		var item serverAgentOutboundItem
		select {
		case <-outbound.done:
			return
		case item = <-outbound.priority:
		default:
			select {
			case <-outbound.done:
				return
			case item = <-outbound.priority:
			case item = <-outbound.queue:
			}
		}
		select {
		case <-outbound.done:
			return
		default:
		}
		err := outbound.socket.SetWriteDeadline(time.Now().Add(serverAgentWebSocketWriteTimeout))
		if err == nil {
			err = outbound.socket.WriteJSON(item.value)
		}
		_ = outbound.socket.SetWriteDeadline(time.Time{})
		if item.result != nil {
			item.result <- err
		}
		if err != nil {
			outbound.fail(err)
			return
		}
	}
}

func (outbound *serverAgentOutbound) fail(err error) {
	outbound.failOnce.Do(func() {
		if outbound.onFailure != nil {
			outbound.onFailure(err)
		}
		outbound.Close()
	})
}

func (outbound *serverAgentOutbound) Close() {
	if outbound == nil {
		return
	}
	outbound.closeOnce.Do(func() { close(outbound.done) })
}
