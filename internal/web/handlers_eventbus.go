package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/eventbus"
	"github.com/asheshgoplani/agent-deck/internal/logging"
	"github.com/gorilla/websocket"
)

// handleEventBusWS upgrades an HTTP request to a WebSocket connection and
// registers the client with the EventBus Hub for channel-based event routing.
func (s *Server) handleEventBusWS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	if !s.authorizeRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	webLog := logging.ForComponent(logging.CompWeb)

	clientID := s.eventHub.RegisterClient(conn)
	webLog.Info("eventbus_client_connected", slog.String("client_id", clientID))
	defer func() {
		s.eventHub.UnregisterClient(clientID)
		webLog.Info("eventbus_client_disconnected", slog.String("client_id", clientID))
	}()

	// Send a welcome message so the client knows the connection is ready.
	_ = conn.WriteJSON(eventbus.ServerMessage{Type: "connected"})

	// Start a heartbeat goroutine that sends periodic pings to keep the
	// connection alive and detect stale clients.
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-s.baseCtx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				if err := conn.WriteJSON(eventbus.ServerMessage{Type: "heartbeat"}); err != nil {
					return
				}
			}
		}
	}()

	// Read loop: dispatch incoming messages to the Hub.
	for {
		_, payload, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(
				err,
				websocket.CloseNormalClosure,
				websocket.CloseGoingAway,
				websocket.CloseNoStatusReceived,
			) {
				webLog.Warn("eventbus_ws_closed_unexpectedly",
					slog.String("client_id", clientID),
					slog.String("error", err.Error()))
			}
			return
		}

		if err := s.eventHub.HandleMessage(clientID, json.RawMessage(payload)); err != nil {
			webLog.Debug("eventbus_message_error",
				slog.String("client_id", clientID),
				slog.String("error", err.Error()))
			_ = conn.WriteJSON(eventbus.ServerMessage{
				Type: "error",
				Data: err.Error(),
			})
		}
	}
}
