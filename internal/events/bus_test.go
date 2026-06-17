package events

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPublishFanOut(t *testing.T) {
	b := NewBus()
	ch1, cancel1 := b.Subscribe()
	ch2, cancel2 := b.Subscribe()
	defer cancel1()
	defer cancel2()

	b.Publish("agent.registered", map[string]string{"id": "a1"})

	for _, ch := range []<-chan Event{ch1, ch2} {
		select {
		case e := <-ch:
			require.Equal(t, "agent.registered", e.Type)
			require.False(t, e.TS.IsZero())
		case <-time.After(time.Second):
			t.Fatal("no event delivered")
		}
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	b := NewBus()
	ch, cancel := b.Subscribe()
	cancel()
	b.Publish("x", nil) // must not panic or block
	select {
	case _, ok := <-ch:
		require.False(t, ok) // channel closed by cancel
	case <-time.After(time.Second):
		// also acceptable: nothing delivered
	}
}

func TestSlowSubscriberDoesNotBlock(t *testing.T) {
	b := NewBus()
	_, cancel := b.Subscribe() // never drained
	defer cancel()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			b.Publish("flood", i) // must not block on the full subscriber
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a slow subscriber")
	}
}
