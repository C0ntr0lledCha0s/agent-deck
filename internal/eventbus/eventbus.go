// Package eventbus provides an in-memory publish/subscribe event bus
// with panic isolation and concurrent-safe access.
package eventbus

import "sync"

// EventType identifies the kind of event being emitted.
type EventType string

const (
	EventSessionStatusChanged EventType = "session.status_changed"
	EventSessionCreated       EventType = "session.created"
	EventSessionUpdated       EventType = "session.updated"
	EventSessionRemoved       EventType = "session.removed"
	EventTaskCreated          EventType = "task.created"
	EventTaskUpdated          EventType = "task.updated"
	EventTaskRemoved          EventType = "task.removed"
	EventPushSent             EventType = "push.sent"
	EventPushDismissed        EventType = "push.dismissed"
	EventUploadProgress       EventType = "upload.progress"
	EventUploadComplete       EventType = "upload.complete"
	EventHeartbeat            EventType = "heartbeat"
)

// Event is a single message emitted on the bus.
type Event struct {
	Type    EventType
	Channel string
	Data    interface{}
}

// Handler is a callback invoked when an event is emitted.
type Handler func(Event)

// EventBus is a concurrent-safe, in-memory publish/subscribe dispatcher.
type EventBus struct {
	mu          sync.RWMutex
	subscribers map[int]Handler
	nextID      int
}

// New creates a ready-to-use EventBus.
func New() *EventBus {
	return &EventBus{
		subscribers: make(map[int]Handler),
	}
}

// Subscribe registers a handler that will be called for every emitted event.
// It returns an unsubscribe function that removes the handler.
func (b *EventBus) Subscribe(handler Handler) func() {
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	b.subscribers[id] = handler
	b.mu.Unlock()

	return func() {
		b.mu.Lock()
		delete(b.subscribers, id)
		b.mu.Unlock()
	}
}

// Emit dispatches an event to all current subscribers. Each handler is called
// synchronously in an arbitrary order. A panicking handler is recovered so
// that remaining handlers still execute.
func (b *EventBus) Emit(event Event) {
	b.mu.RLock()
	snapshot := make([]Handler, 0, len(b.subscribers))
	for _, h := range b.subscribers {
		snapshot = append(snapshot, h)
	}
	b.mu.RUnlock()

	for _, h := range snapshot {
		func() {
			defer func() { recover() }()
			h(event)
		}()
	}
}

// SubscriberCount returns the number of active subscribers.
func (b *EventBus) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers)
}
