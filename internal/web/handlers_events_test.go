package web

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMenuEventsUnauthorizedWhenTokenEnabled(t *testing.T) {
	srv := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		Token:      "secret-token",
	})
	srv.menuData = &fakeMenuDataLoader{
		snapshot: &MenuSnapshot{Profile: "default"},
	}

	req := httptest.NewRequest(http.MethodGet, "/events/menu", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"code":"UNAUTHORIZED"`) {
		t.Fatalf("expected UNAUTHORIZED body, got: %s", rr.Body.String())
	}
}

func TestMenuEventsReturnsDeprecationNotice(t *testing.T) {
	srv := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
	})
	srv.menuData = &fakeMenuDataLoader{
		snapshot: &MenuSnapshot{Profile: "default"},
	}

	testServer := httptest.NewServer(srv.Handler())
	defer testServer.Close()

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(testServer.URL + "/events/menu")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("expected text/event-stream content-type, got: %s", ct)
	}

	reader := bufio.NewReader(resp.Body)
	event, payload, err := readSSEEvent(reader)
	if err != nil {
		t.Fatalf("failed to read sse event: %v", err)
	}
	if event != "deprecated" {
		t.Fatalf("expected event 'deprecated', got %q", event)
	}

	var msg map[string]any
	if err := json.Unmarshal([]byte(payload), &msg); err != nil {
		t.Fatalf("invalid deprecation payload: %v", err)
	}
	if msg["deprecated"] != true {
		t.Fatalf("expected deprecated=true, got: %v", msg["deprecated"])
	}
	if msg["message"] != "Use /ws/events WebSocket instead" {
		t.Fatalf("unexpected deprecation message: %v", msg["message"])
	}
}

func readSSEEvent(r *bufio.Reader) (string, string, error) {
	var (
		event string
		data  string
	)

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if event != "" || data != "" {
				return event, data, nil
			}
			return "", "", err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if event != "" || data != "" {
				return event, data, nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}
}
