package eventbus

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockConn implements WSConn for testing.
type mockConn struct {
	mu       sync.Mutex
	messages []any
}

func (m *mockConn) WriteJSON(v any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, v)
	return nil
}

func (m *mockConn) lastMessage() any {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.messages) == 0 {
		return nil
	}
	return m.messages[len(m.messages)-1]
}

func (m *mockConn) messageCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.messages)
}

// --- Protocol parsing tests ---

func TestProtocol_ParseSubscribe(t *testing.T) {
	raw := json.RawMessage(`{"type":"subscribe","channel":"sessions"}`)
	msg, err := ParseClientMessage(raw)
	require.NoError(t, err)
	assert.Equal(t, "subscribe", msg.Type)
	assert.Equal(t, "sessions", msg.Channel)
}

func TestProtocol_ParseSubscribeSession(t *testing.T) {
	raw := json.RawMessage(`{"type":"subscribe","channel":"session","sessionId":"abc-123"}`)
	msg, err := ParseClientMessage(raw)
	require.NoError(t, err)
	assert.Equal(t, "subscribe", msg.Type)
	assert.Equal(t, "session", msg.Channel)
	assert.Equal(t, "abc-123", msg.SessionID)
}

func TestProtocol_ParseUnsubscribe(t *testing.T) {
	raw := json.RawMessage(`{"type":"unsubscribe","subscriptionId":"sub-1"}`)
	msg, err := ParseClientMessage(raw)
	require.NoError(t, err)
	assert.Equal(t, "unsubscribe", msg.Type)
	assert.Equal(t, "sub-1", msg.SubscriptionID)
}

func TestProtocol_ParsePing(t *testing.T) {
	raw := json.RawMessage(`{"type":"ping"}`)
	msg, err := ParseClientMessage(raw)
	require.NoError(t, err)
	assert.Equal(t, "ping", msg.Type)
}

func TestProtocol_ParseInvalid(t *testing.T) {
	raw := json.RawMessage(`not valid json`)
	_, err := ParseClientMessage(raw)
	require.Error(t, err)
}

func TestProtocol_MarshalEvent(t *testing.T) {
	msg := ServerMessage{
		Type:      "event",
		Channel:   "sessions",
		EventType: "status-changed",
		Data:      map[string]string{"id": "s1", "status": "running"},
	}

	data, err := json.Marshal(msg)
	require.NoError(t, err)

	var decoded map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, "event", decoded["type"])
	assert.Equal(t, "sessions", decoded["channel"])
	assert.Equal(t, "status-changed", decoded["eventType"])
	assert.NotNil(t, decoded["data"])
}

func TestProtocol_MarshalPong(t *testing.T) {
	msg := ServerMessage{Type: "pong"}
	data, err := json.Marshal(msg)
	require.NoError(t, err)

	var decoded map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, "pong", decoded["type"])
}

// --- Hub client tracking tests ---

func TestHub_ClientTracking(t *testing.T) {
	bus := New()
	hub := NewHub(bus)
	defer hub.Close()

	assert.Equal(t, 0, hub.ClientCount())

	conn := &mockConn{}
	clientID := hub.RegisterClient(conn)
	assert.NotEmpty(t, clientID)
	assert.Equal(t, 1, hub.ClientCount())

	ids := hub.ConnectedClientIDs()
	assert.Contains(t, ids, clientID)

	hub.UnregisterClient(clientID)
	assert.Equal(t, 0, hub.ClientCount())
}

func TestHub_MultipleClients(t *testing.T) {
	bus := New()
	hub := NewHub(bus)
	defer hub.Close()

	conn1 := &mockConn{}
	conn2 := &mockConn{}

	id1 := hub.RegisterClient(conn1)
	id2 := hub.RegisterClient(conn2)

	assert.Equal(t, 2, hub.ClientCount())
	assert.NotEqual(t, id1, id2)

	hub.UnregisterClient(id1)
	assert.Equal(t, 1, hub.ClientCount())

	ids := hub.ConnectedClientIDs()
	assert.NotContains(t, ids, id1)
	assert.Contains(t, ids, id2)
}

func TestHub_UnregisterIdempotent(t *testing.T) {
	bus := New()
	hub := NewHub(bus)
	defer hub.Close()

	conn := &mockConn{}
	id := hub.RegisterClient(conn)

	hub.UnregisterClient(id)
	hub.UnregisterClient(id) // should not panic
	assert.Equal(t, 0, hub.ClientCount())
}

// --- Hub message handling tests ---

func TestHub_HandlePing(t *testing.T) {
	bus := New()
	hub := NewHub(bus)
	defer hub.Close()

	conn := &mockConn{}
	clientID := hub.RegisterClient(conn)

	raw := json.RawMessage(`{"type":"ping"}`)
	err := hub.HandleMessage(clientID, raw)
	require.NoError(t, err)

	require.Equal(t, 1, conn.messageCount())
	msg, ok := conn.lastMessage().(*ServerMessage)
	require.True(t, ok)
	assert.Equal(t, "pong", msg.Type)
}

func TestHub_HandleSubscribeAndBroadcast(t *testing.T) {
	bus := New()
	hub := NewHub(bus)
	defer hub.Close()

	conn := &mockConn{}
	clientID := hub.RegisterClient(conn)

	// Subscribe to sessions channel
	raw := json.RawMessage(`{"type":"subscribe","channel":"sessions"}`)
	err := hub.HandleMessage(clientID, raw)
	require.NoError(t, err)

	// The subscribe response should contain a subscriptionId
	require.GreaterOrEqual(t, conn.messageCount(), 1)

	// Emit a session event on the bus
	bus.Emit(Event{
		Type:    EventSessionCreated,
		Channel: "s1",
		Data:    map[string]string{"id": "s1"},
	})

	// Client should receive the event
	require.GreaterOrEqual(t, conn.messageCount(), 2)
}

func TestHub_HandleUnsubscribe(t *testing.T) {
	bus := New()
	hub := NewHub(bus)
	defer hub.Close()

	conn := &mockConn{}
	clientID := hub.RegisterClient(conn)

	// Subscribe to sessions channel
	raw := json.RawMessage(`{"type":"subscribe","channel":"sessions"}`)
	err := hub.HandleMessage(clientID, raw)
	require.NoError(t, err)

	// Get the subscription ID from the response
	require.GreaterOrEqual(t, conn.messageCount(), 1)
	subResp, ok := conn.messages[0].(*ServerMessage)
	require.True(t, ok)
	subID := subResp.SubscriptionID

	// Unsubscribe
	unsubRaw := json.RawMessage(`{"type":"unsubscribe","subscriptionId":"` + subID + `"}`)
	err = hub.HandleMessage(clientID, unsubRaw)
	require.NoError(t, err)

	countBefore := conn.messageCount()

	// Emit a session event — client should NOT receive it
	bus.Emit(Event{
		Type:    EventSessionCreated,
		Channel: "s1",
		Data:    map[string]string{"id": "s1"},
	})

	assert.Equal(t, countBefore, conn.messageCount(), "should not receive events after unsubscribe")
}

func TestHub_SessionChannelRouting(t *testing.T) {
	bus := New()
	hub := NewHub(bus)
	defer hub.Close()

	conn := &mockConn{}
	clientID := hub.RegisterClient(conn)

	// Subscribe to a specific session channel
	raw := json.RawMessage(`{"type":"subscribe","channel":"session","sessionId":"abc-123"}`)
	err := hub.HandleMessage(clientID, raw)
	require.NoError(t, err)

	// Emit an event for the matching session
	bus.Emit(Event{
		Type:    EventSessionStatusChanged,
		Channel: "abc-123",
		Data:    map[string]string{"status": "running"},
	})

	// Should receive this event (subscribe response + event)
	require.GreaterOrEqual(t, conn.messageCount(), 2)

	// Emit an event for a different session
	countBefore := conn.messageCount()
	bus.Emit(Event{
		Type:    EventSessionStatusChanged,
		Channel: "different-session",
		Data:    map[string]string{"status": "idle"},
	})

	// Should NOT receive this event
	assert.Equal(t, countBefore, conn.messageCount(), "should not receive events for other sessions")
}

func TestHub_EventChannelMapping(t *testing.T) {
	tests := []struct {
		eventType EventType
		channel   string
	}{
		{EventSessionStatusChanged, "sessions"},
		{EventSessionCreated, "sessions"},
		{EventSessionUpdated, "sessions"},
		{EventSessionRemoved, "sessions"},
		{EventTaskCreated, "tasks"},
		{EventTaskUpdated, "tasks"},
		{EventTaskRemoved, "tasks"},
		{EventPushSent, "push"},
		{EventPushDismissed, "push"},
		{EventUploadProgress, "uploads"},
		{EventUploadComplete, "uploads"},
		{EventHeartbeat, "system"},
	}

	for _, tt := range tests {
		t.Run(string(tt.eventType), func(t *testing.T) {
			assert.Equal(t, tt.channel, eventChannel(tt.eventType))
		})
	}
}

func TestHub_HandleMessageUnknownClient(t *testing.T) {
	bus := New()
	hub := NewHub(bus)
	defer hub.Close()

	raw := json.RawMessage(`{"type":"ping"}`)
	err := hub.HandleMessage("nonexistent", raw)
	require.Error(t, err)
}

func TestHub_HandleMessageUnknownType(t *testing.T) {
	bus := New()
	hub := NewHub(bus)
	defer hub.Close()

	conn := &mockConn{}
	clientID := hub.RegisterClient(conn)

	raw := json.RawMessage(`{"type":"invalid"}`)
	err := hub.HandleMessage(clientID, raw)
	require.Error(t, err)
}

func TestHub_HandleSubscribeInvalidChannel(t *testing.T) {
	bus := New()
	hub := NewHub(bus)
	defer hub.Close()

	conn := &mockConn{}
	clientID := hub.RegisterClient(conn)

	raw := json.RawMessage(`{"type":"subscribe","channel":"bogus"}`)
	err := hub.HandleMessage(clientID, raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown channel")
}

func TestHub_Close(t *testing.T) {
	bus := New()
	hub := NewHub(bus)

	conn := &mockConn{}
	hub.RegisterClient(conn)
	assert.Equal(t, 1, hub.ClientCount())

	hub.Close()
	assert.Equal(t, 0, hub.ClientCount())
}
