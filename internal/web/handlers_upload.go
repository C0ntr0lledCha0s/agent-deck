package web

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/logging"
	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/gorilla/websocket"
)

const maxUploadSize = 100 * 1024 * 1024 // 100 MB

// uploadStartMsg is sent by the client to begin an upload.
type uploadStartMsg struct {
	Type     string `json:"type"` // "start"
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
}

// uploadProgressMsg is sent by the server to report progress.
type uploadProgressMsg struct {
	Type     string `json:"type"` // "progress"
	Received int64  `json:"received"`
	Total    int64  `json:"total"`
}

// uploadCompleteMsg is sent by the server when the upload finishes.
type uploadCompleteMsg struct {
	Type     string `json:"type"` // "complete"
	Path     string `json:"path"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
}

func (s *Server) handleUploadWS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	if !s.authorizeRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}

	if s.cfg.ReadOnly {
		writeAPIError(w, http.StatusForbidden, "READ_ONLY", "server is in read-only mode")
		return
	}

	const prefix = "/ws/upload/"
	sessionID := strings.TrimPrefix(r.URL.Path, prefix)
	if sessionID == "" || strings.Contains(sessionID, "/") {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "session id is required")
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	webLog := logging.ForComponent(logging.CompWeb)

	var (
		file         *os.File
		filePath     string
		totalSize    int64
		received     int64
		lastProgress int64
		completed    bool
	)

	// On disconnect, close the file and remove partial uploads.
	defer func() {
		if file != nil {
			file.Close()
		}
		if !completed && filePath != "" {
			_ = os.Remove(filePath)
		}
	}()

	const progressInterval int64 = 64 * 1024 // send progress every 64KB

	for {
		msgType, payload, readErr := conn.ReadMessage()
		if readErr != nil {
			if websocket.IsUnexpectedCloseError(
				readErr,
				websocket.CloseNormalClosure,
				websocket.CloseGoingAway,
				websocket.CloseNoStatusReceived,
			) {
				webLog.Warn("upload_ws_closed_unexpectedly",
					slog.String("session_id", sessionID),
					slog.String("error", readErr.Error()))
			}
			return
		}

		switch msgType {
		case websocket.TextMessage:
			var raw struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(payload, &raw); err != nil {
				_ = writeWSJSON(conn, map[string]string{
					"type":    "error",
					"message": "invalid json",
				})
				continue
			}

			switch raw.Type {
			case "start":
				// Close and discard any prior in-progress upload.
				if file != nil {
					file.Close()
					file = nil
					if filePath != "" {
						_ = os.Remove(filePath)
						filePath = ""
					}
				}

				var startMsg uploadStartMsg
				if err := json.Unmarshal(payload, &startMsg); err != nil {
					_ = writeWSJSON(conn, map[string]string{
						"type":    "error",
						"message": "invalid start message",
					})
					continue
				}

				if startMsg.Size > maxUploadSize {
					_ = writeWSJSON(conn, map[string]string{
						"type":    "error",
						"message": fmt.Sprintf("file too large: %d bytes exceeds %d byte limit", startMsg.Size, maxUploadSize),
					})
					continue
				}

				if startMsg.Size <= 0 {
					_ = writeWSJSON(conn, map[string]string{
						"type":    "error",
						"message": "invalid file size",
					})
					continue
				}

				safeName := sanitizeFilename(startMsg.Filename)
				totalSize = startMsg.Size
				received = 0
				lastProgress = 0
				completed = false

				// Resolve upload directory.
				profileDir, dirErr := session.GetProfileDir(session.GetEffectiveProfile(s.cfg.Profile))
				if dirErr != nil {
					_ = writeWSJSON(conn, map[string]string{
						"type":    "error",
						"message": "failed to resolve upload directory",
					})
					webLog.Error("upload_profile_dir", slog.String("error", dirErr.Error()))
					continue
				}

				uploadDir := filepath.Join(profileDir, "uploads", sessionID)
				if mkErr := os.MkdirAll(uploadDir, 0700); mkErr != nil {
					_ = writeWSJSON(conn, map[string]string{
						"type":    "error",
						"message": "failed to create upload directory",
					})
					webLog.Error("upload_mkdir", slog.String("error", mkErr.Error()))
					continue
				}

				uuid := generateUUID()
				filePath = filepath.Join(uploadDir, uuid+"-"+safeName)

				file, err = os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
				if err != nil {
					_ = writeWSJSON(conn, map[string]string{
						"type":    "error",
						"message": "failed to create file",
					})
					webLog.Error("upload_create_file", slog.String("error", err.Error()))
					filePath = ""
					continue
				}

				webLog.Info("upload_started",
					slog.String("session_id", sessionID),
					slog.String("filename", safeName),
					slog.Int64("size", totalSize))

			case "end":
				if file == nil || filePath == "" {
					_ = writeWSJSON(conn, map[string]string{
						"type":    "error",
						"message": "no upload in progress",
					})
					continue
				}
				file.Close()
				file = nil
				completed = true
				_ = writeWSJSON(conn, uploadCompleteMsg{
					Type:     "complete",
					Path:     filePath,
					Filename: filepath.Base(filePath),
					Size:     received,
				})
				webLog.Info("upload_complete",
					slog.String("session_id", sessionID),
					slog.String("path", filePath),
					slog.Int64("size", received))

				// Reset for potential next upload on same connection.
				filePath = ""
				received = 0
				totalSize = 0
			}

		case websocket.BinaryMessage:
			if file == nil {
				_ = writeWSJSON(conn, map[string]string{
					"type":    "error",
					"message": "no upload in progress",
				})
				continue
			}

			chunkSize := int64(len(payload))
			if received+chunkSize > maxUploadSize {
				_ = writeWSJSON(conn, map[string]string{
					"type":    "error",
					"message": "upload exceeds maximum size",
				})
				file.Close()
				file = nil
				_ = os.Remove(filePath)
				filePath = ""
				continue
			}

			if _, writeErr := file.Write(payload); writeErr != nil {
				_ = writeWSJSON(conn, map[string]string{
					"type":    "error",
					"message": "failed to write chunk",
				})
				webLog.Error("upload_write_chunk", slog.String("error", writeErr.Error()))
				file.Close()
				file = nil
				_ = os.Remove(filePath)
				filePath = ""
				continue
			}

			received += chunkSize

			if received-lastProgress >= progressInterval || received >= totalSize {
				_ = writeWSJSON(conn, uploadProgressMsg{
					Type:     "progress",
					Received: received,
					Total:    totalSize,
				})
				lastProgress = received
			}
		}
	}
}

// sanitizeFilename strips path traversal characters and returns a safe filename.
func sanitizeFilename(name string) string {
	// Remove path separators and traversal components.
	name = strings.ReplaceAll(name, "..", "")
	name = strings.ReplaceAll(name, "/", "")
	name = strings.ReplaceAll(name, "\\", "")
	name = strings.TrimSpace(name)
	if name == "" {
		return "unnamed"
	}
	// Cap length to stay within filesystem NAME_MAX (255) minus UUID prefix.
	const maxFilenameLen = 200
	if len(name) > maxFilenameLen {
		name = name[:maxFilenameLen]
	}
	return name
}

// generateUUID produces a random hex string suitable for unique file prefixes.
func generateUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based name on rand failure.
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// writeWSJSON writes a JSON message to the WebSocket connection.
func writeWSJSON(conn *websocket.Conn, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, data)
}
