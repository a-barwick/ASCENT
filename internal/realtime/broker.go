// Package realtime provides process-local wakeups for committed PostgreSQL
// events. It never stores authoritative facts: subscribers always reconcile
// from the ordered outbox after receiving a wakeup or reconnecting.
package realtime

import (
	"context"
	"sync"
)

type Broker struct {
	mu          sync.Mutex
	nextID      uint64
	subscribers map[uint64]chan struct{}
}

func NewBroker() *Broker {
	return &Broker{subscribers: make(map[uint64]chan struct{})}
}

// Subscribe returns a coalescing wakeup channel. Missing a wakeup is safe
// because callers query committed events using their last durable cursor.
func (b *Broker) Subscribe(ctx context.Context) <-chan struct{} {
	channel := make(chan struct{}, 1)

	b.mu.Lock()
	id := b.nextID
	b.nextID++
	b.subscribers[id] = channel
	b.mu.Unlock()

	go func() {
		<-ctx.Done()
		b.mu.Lock()
		delete(b.subscribers, id)
		close(channel)
		b.mu.Unlock()
	}()

	return channel
}

func (b *Broker) NotifyCommitted() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, subscriber := range b.subscribers {
		select {
		case subscriber <- struct{}{}:
		default:
		}
	}
}
