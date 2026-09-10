package fs

import "sync"

// taskSubscription decouples a manager mutation from a potentially slow SSE writer
// without dropping any of the complete snapshots accepted by the legacy contract.
type taskSubscription[T any] struct {
	output chan []T
	done   chan struct{}

	mu      sync.Mutex
	changed *sync.Cond
	pending [][]T
	closed  bool
}

func newTaskSubscription[T any](initial []T) *taskSubscription[T] {
	subscription := &taskSubscription[T]{output: make(chan []T), done: make(chan struct{}), pending: [][]T{initial}}
	subscription.changed = sync.NewCond(&subscription.mu)
	go subscription.run()
	return subscription
}

func (subscription *taskSubscription[T]) publish(snapshot []T) {
	subscription.mu.Lock()
	defer subscription.mu.Unlock()
	if subscription.closed {
		return
	}
	subscription.pending = append(subscription.pending, snapshot)
	subscription.changed.Signal()
}

func (subscription *taskSubscription[T]) close() {
	subscription.mu.Lock()
	if !subscription.closed {
		subscription.closed = true
		close(subscription.done)
		subscription.changed.Broadcast()
	}
	subscription.mu.Unlock()
}

func (subscription *taskSubscription[T]) run() {
	defer close(subscription.output)
	for {
		subscription.mu.Lock()
		for len(subscription.pending) == 0 && !subscription.closed {
			subscription.changed.Wait()
		}
		if subscription.closed {
			subscription.mu.Unlock()
			return
		}
		snapshot := subscription.pending[0]
		subscription.pending = subscription.pending[1:]
		subscription.mu.Unlock()
		select {
		case subscription.output <- snapshot:
		case <-subscription.done:
			return
		}
	}
}
