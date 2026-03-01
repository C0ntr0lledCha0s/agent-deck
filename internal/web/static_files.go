package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/highlight"
)

//go:embed static/*
var embeddedStaticFiles embed.FS

func (s *Server) staticFileServer() http.Handler {
	sub, err := fs.Sub(embeddedStaticFiles, "static")
	if err != nil {
		// This should never happen with embedded files present at build time.
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "static assets unavailable", http.StatusInternalServerError)
		})
	}
	return http.FileServer(http.FS(sub))
}

// handleIndex serves the dashboard as the default landing page.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	path := r.URL.Path
	if path != "/" && !strings.HasPrefix(path, "/s/") {
		http.NotFound(w, r)
		return
	}

	dashboard, err := embeddedStaticFiles.ReadFile("static/dashboard.html")
	if err != nil {
		http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(dashboard)
}

// handleTerminal serves the existing terminal-focused UI.
func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	if r.URL.Path != "/terminal" {
		http.NotFound(w, r)
		return
	}

	index, err := embeddedStaticFiles.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "terminal unavailable", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(index)
}

func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/manifest.webmanifest" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if err := serveEmbeddedFile(
		w,
		"static/manifest.webmanifest",
		"application/manifest+json; charset=utf-8",
		map[string]string{
			"Cache-Control": "no-cache",
		},
	); err != nil {
		http.Error(w, "manifest unavailable", http.StatusInternalServerError)
	}
}

func (s *Server) handleServiceWorker(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/sw.js" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if err := serveEmbeddedFile(
		w,
		"static/sw.js",
		"application/javascript; charset=utf-8",
		map[string]string{
			"Cache-Control":          "no-cache",
			"Service-Worker-Allowed": "/",
		},
	); err != nil {
		http.Error(w, "service worker unavailable", http.StatusInternalServerError)
	}
}

// syntaxCSS is computed once at import time from the highlight package.
var syntaxCSS = []byte(highlight.CSSVariables())

// handleSyntaxCSS serves the Chroma syntax highlighting CSS needed by
// server-rendered highlighted code (Read tool, code blocks).
func (s *Server) handleSyntaxCSS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(syntaxCSS)
}

// handleHighlight accepts a batch of code snippets and returns syntax-highlighted
// HTML using Chroma. POST /api/highlight with JSON body:
//
//	{"blocks": [{"code": "...", "language": "go"}, ...]}
//
// Returns: {"blocks": [{"html": "<span class=\"chroma\">...</span>"}, ...]}
func (s *Server) handleHighlight(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	if !s.authorizeRequest(r) {
		writeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 2*1024*1024) // 2 MB limit

	var req struct {
		Blocks []struct {
			Code     string `json:"code"`
			Language string `json:"language"`
		} `json:"blocks"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid JSON")
		return
	}

	type resultBlock struct {
		HTML string `json:"html"`
	}
	results := make([]resultBlock, len(req.Blocks))
	for i, b := range req.Blocks {
		lang := b.Language
		if lang == "" {
			lang = "plaintext"
		}
		highlighted, err := highlight.Code(b.Code, lang)
		if err != nil {
			results[i] = resultBlock{HTML: escapeHTML(b.Code)}
			continue
		}
		results[i] = resultBlock{HTML: highlighted}
	}

	writeJSON(w, http.StatusOK, map[string]any{"blocks": results})
}

func serveEmbeddedFile(w http.ResponseWriter, path, contentType string, headers map[string]string) error {
	body, err := embeddedStaticFiles.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read embedded file %q: %w", path, err)
	}

	for key, value := range headers {
		if value == "" {
			continue
		}
		w.Header().Set(key, value)
	}
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
	return nil
}
