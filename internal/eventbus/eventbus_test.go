package eventbus

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventBus_SubscribeAndEmit(t *testing.T) {
	bus := New()

	var received []Event
	var mu sync.Mutex

	bus.Subscribe(func(e Event) {
		mu.Lock()
		received = append(received, e)
		mu.Unlock()
	})

	bus.Emit(Event{Type: EventSessionCreated, Channel: "s1", Data: "first"})
	bus.Emit(Event{Type: EventSessionUpdated, Channel: "s1", Data: "second"})

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, received, 2)
	assert.Equal(t, EventSessionCreated, received[0].Type)
	assert.Equal(t, "first", received[0].Data)
	assert.Equal(t, EventSessionUpdated, received[1].Type)
	assert.Equal(t, "second", received[1].Data)
}

func TestEventBus_Unsubscribe(t *testing.T) {
	bus := New()

	var count atomic.Int64

	unsub := bus.Subscribe(func(e Event) {
		count.Add(1)
	})

	bus.Emit(Event{Type: EventSessionCreated, Channel: "s1"})
	assert.Equal(t, int64(1), count.Load())

	unsub()

	bus.Emit(Event{Type: EventSessionUpdated, Channel: "s1"})
	assert.Equal(t, int64(1), count.Load(), "should not receive after unsubscribe")
}

func TestEventBus_ErrorIsolation(t *testing.T) {
	bus := New()

	var received atomic.Int64

	// First subscriber panics
	bus.Subscribe(func(e Event) {
		panic("boom")
	})

	// Second subscriber should still receive the event
	bus.Subscribe(func(e Event) {
		received.Add(1)
	})

	bus.Emit(Event{Type: EventSessionCreated, Channel: "s1"})

	assert.Equal(t, int64(1), received.Load(), "second subscriber should receive event despite first panicking")
}

func TestEventBus_ConcurrentAccess(t *testing.T) {
	bus := New()

	var received atomic.Int64

	bus.Subscribe(func(e Event) {
		received.Add(1)
	})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bus.Emit(Event{Type: EventHeartbeat, Channel: "test"})
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(100), received.Load(), "should receive all 100 events")
}
