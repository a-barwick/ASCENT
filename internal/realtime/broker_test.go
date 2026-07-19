package realtime

import (
	"context"
	"testing"
	"time"
)

func TestBrokerCoalescesWakeupsAndClosesOnContext(t *testing.T) {
	t.Parallel()

	broker := NewBroker()
	ctx, cancel := context.WithCancel(context.Background())
	channel := broker.Subscribe(ctx)

	broker.NotifyCommitted()
	broker.NotifyCommitted()
	select {
	case <-channel:
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive wakeup")
	}

	select {
	case <-channel:
		t.Fatal("wakeup channel did not coalesce notifications")
	default:
	}

	cancel()
	select {
	case _, open := <-channel:
		if open {
			t.Fatal("channel remained open")
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not close")
	}
}
