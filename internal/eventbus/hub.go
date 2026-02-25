package eventbus

import (
	"encoding/json"
	"fmt"
	"sync"
)

// ClientMessage represents a message sent from a WebSocket client to the server.
type ClientMessage struct {
	Type           string `json:"type"`
	Channel        string `json:"channel,omitempty"`
	SessionID      string `json:"sessionId,omitempty"`
	SubscriptionID string `json:"subscriptionId,omitempty"`
}

// ServerMessage represents a message sent from the server to a WebSocket client.
type ServerMessage struct {
	Type           string `json:"type"`
	Channel        string `json:"channel,omitempty"`
	EventType      string `json:"eventType,omitempty"`
	SubscriptionID string `json:"subscriptionId,omitempty"`
	Data           any    `json:"data,omitempty"`
}

// ParseClientMessage decodes a raw JSON message into a ClientMessage.
func ParseClientMessage(raw json.RawMessage) (*ClientMessage, error) {
	var msg ClientMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return nil, fmt.Errorf("eventbus: invalid client message: %w", err)
	}
	return &msg, nil
}

// WSConn is the interface required for a WebSocket connection.
// It is intentionally minimal to allow easy testing with mocks.
type WSConn interface {
	WriteJSON(v interface{}) error
}

// subscription tracks one client subscription to a channel.
type subscription struct {
	channel   string // "sessions", "tasks", "push", "uploads", "system"
	sessionID string // non-empty only for per-session subscriptions (channel == "session")
}

// client tracks a single connected WebSocket client.
type client struct {
	conn          WSConn
	subscriptions map[string]subscription // subscriptionID -> subscription
}

// Hub manages WebSocket clients and routes EventBus events to them
// based on their channel subscriptions.
type Hub struct {
	mu      sync.RWMutex
	bus     *EventBus
	clients map[string]*client // clientID -> client
	nextID  int
	nextSub int
	unsub   func() // unsubscribe from EventBus
}

// NewHub creates a Hub that subscribes to the given EventBus and
// forwards matching events to connected WebSocket clients.
func NewHub(bus *EventBus) *Hub {
	h := &Hub{
		bus:     bus,
		clients: make(map[string]*client),
	}
	h.unsub = bus.Subscribe(func(e Event) {
		h.broadcast(e)
	})
	return h
}

// RegisterClient adds a WebSocket connection to the hub and returns
// a unique client ID used for subsequent operations.
func (h *Hub) RegisterClient(conn WSConn) string {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.nextID++
	id := fmt.Sprintf("client-%d", h.nextID)
	h.clients[id] = &client{
		conn:          conn,
		subscriptions: make(map[string]subscription),
	}
	return id
}

// UnregisterClient removes a client and all its subscriptions.
// It is safe to call with an unknown client ID.
func (h *Hub) UnregisterClient(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, id)
}

// HandleMessage processes a raw JSON message from a client.
// It dispatches on the message type: subscribe, unsubscribe, ping.
func (h *Hub) HandleMessage(clientID string, raw json.RawMessage) error {
	msg, err := ParseClientMessage(raw)
	if err != nil {
		return err
	}

	h.mu.Lock()
	c, ok := h.clients[clientID]
	h.mu.Unlock()
	if !ok {
		return fmt.Errorf("eventbus: unknown client %q", clientID)
	}

	switch msg.Type {
	case "subscribe":
		return h.handleSubscribe(c, msg)
	case "unsubscribe":
		return h.handleUnsubscribe(c, msg)
	case "ping":
		return c.conn.WriteJSON(&ServerMessage{Type: "pong"})
	default:
		return fmt.Errorf("eventbus: unknown message type %q", msg.Type)
	}
}

// handleSubscribe creates a new subscription for the client and sends
// back a confirmation with the subscription ID.
func (h *Hub) handleSubscribe(c *client, msg *ClientMessage) error {
	h.mu.Lock()
	h.nextSub++
	subID := fmt.Sprintf("sub-%d", h.nextSub)
	c.subscriptions[subID] = subscription{
		channel:   msg.Channel,
		sessionID: msg.SessionID,
	}
	h.mu.Unlock()

	return c.conn.WriteJSON(&ServerMessage{
		Type:           "subscribed",
		Channel:        msg.Channel,
		SubscriptionID: subID,
	})
}

// handleUnsubscribe removes a subscription by its ID.
func (h *Hub) handleUnsubscribe(c *client, msg *ClientMessage) error {
	h.mu.Lock()
	delete(c.subscriptions, msg.SubscriptionID)
	h.mu.Unlock()
	return nil
}

// broadcast routes an EventBus event to all clients that have a matching subscription.
func (h *Hub) broadcast(event Event) {
	ch := eventChannel(event.Type)

	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, c := range h.clients {
		if h.clientWantsEvent(c, ch, event) {
			// Fire-and-forget: errors are silently dropped since the client
			// will be cleaned up on the next write failure by the caller.
			_ = c.conn.WriteJSON(&ServerMessage{
				Type:      "event",
				Channel:   ch,
				EventType: wireEventType(event.Type),
				Data:      event.Data,
			})
		}
	}
}

// clientWantsEvent returns true if the client has any subscription matching
// the given channel and event.
func (h *Hub) clientWantsEvent(c *client, ch string, event Event) bool {
	for _, sub := range c.subscriptions {
		// Per-session subscription: channel must be "session" and sessionID must match
		if sub.channel == "session" {
			if sub.sessionID == event.Channel {
				return true
			}
			continue
		}
		// Broad channel subscription
		if sub.channel == ch {
			return true
		}
	}
	return false
}

// eventChannel maps an EventType to the wire-protocol channel name.
func eventChannel(et EventType) string {
	switch et {
	case EventSessionStatusChanged, EventSessionCreated, EventSessionUpdated, EventSessionRemoved:
		return "sessions"
	case EventTaskCreated, EventTaskUpdated, EventTaskRemoved:
		return "tasks"
	case EventPushSent, EventPushDismissed:
		return "push"
	case EventUploadProgress, EventUploadComplete:
		return "uploads"
	case EventHeartbeat:
		return "system"
	default:
		return "system"
	}
}

// wireEventType converts an internal EventType to the wire-protocol event type string.
// For example, "session.status_changed" becomes "status-changed".
func wireEventType(et EventType) string {
	switch et {
	case EventSessionStatusChanged:
		return "status-changed"
	case EventSessionCreated:
		return "created"
	case EventSessionUpdated:
		return "updated"
	case EventSessionRemoved:
		return "removed"
	case EventTaskCreated:
		return "created"
	case EventTaskUpdated:
		return "updated"
	case EventTaskRemoved:
		return "removed"
	case EventPushSent:
		return "sent"
	case EventPushDismissed:
		return "dismissed"
	case EventUploadProgress:
		return "progress"
	case EventUploadComplete:
		return "complete"
	case EventHeartbeat:
		return "heartbeat"
	default:
		return string(et)
	}
}

// ClientCount returns the number of connected clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// ConnectedClientIDs returns the IDs of all connected clients.
func (h *Hub) ConnectedClientIDs() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ids := make([]string, 0, len(h.clients))
	for id := range h.clients {
		ids = append(ids, id)
	}
	return ids
}

// Close unsubscribes the hub from the EventBus and removes all clients.
func (h *Hub) Close() {
	h.unsub()
	h.mu.Lock()
	defer h.mu.Unlock()
	clear(h.clients)
}
