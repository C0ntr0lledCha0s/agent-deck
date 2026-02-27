package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/hub"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTemplatesE2E_FullWorkflow exercises the complete workspace templates
// lifecycle: list built-ins, create user template, create project from template,
// verify workspace shows template, delete user template.
func TestTemplatesE2E_FullWorkflow(t *testing.T) {
	srv := newTestServerWithHub(t)
	handler := srv.Handler()

	// ── Step 1: List templates — should include built-in claude-sandbox ──
	t.Run("list_includes_builtin", func(t *testing.T) {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/templates", nil))
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp templatesListResponse
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
		require.NotEmpty(t, resp.Templates)

		var claudeSandbox *hub.Template
		for _, tmpl := range resp.Templates {
			if tmpl.Name == "claude-sandbox" {
				claudeSandbox = tmpl
			}
		}
		require.NotNil(t, claudeSandbox, "claude-sandbox should be in list")
		assert.True(t, claudeSandbox.BuiltIn)
		assert.Equal(t, "sandbox-image:latest", claudeSandbox.Image)
		assert.Equal(t, 2.0, claudeSandbox.CPUDefault)
		assert.Equal(t, int64(2*1024*1024*1024), claudeSandbox.MemoryDefault)
		assert.Contains(t, claudeSandbox.Tags, "claude")
		assert.Contains(t, claudeSandbox.Tags, "sandbox")
	})

	// ── Step 2: Get single built-in template ──
	t.Run("get_builtin", func(t *testing.T) {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/templates/claude-sandbox", nil))
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp templateDetailResponse
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
		assert.Equal(t, "claude-sandbox", resp.Template.Name)
		assert.True(t, resp.Template.BuiltIn)
	})

	// ── Step 3: Create a user-defined template ──
	t.Run("create_user_template", func(t *testing.T) {
		body := `{
			"name": "python-ml",
			"description": "Python ML development environment",
			"image": "python-ml:latest",
			"cpuDefault": 4,
			"memoryDefault": 8589934592,
			"tags": ["python", "ml", "gpu"]
		}`
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/templates", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusCreated, rr.Code)

		var resp templateDetailResponse
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
		assert.Equal(t, "python-ml", resp.Template.Name)
		assert.Equal(t, "python-ml:latest", resp.Template.Image)
		assert.Equal(t, 4.0, resp.Template.CPUDefault)
		assert.False(t, resp.Template.BuiltIn)
	})

	// ── Step 4: List now includes both built-in and user template ──
	t.Run("list_includes_user_and_builtin", func(t *testing.T) {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/templates", nil))
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp templatesListResponse
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))

		names := make(map[string]bool)
		for _, tmpl := range resp.Templates {
			names[tmpl.Name] = true
		}
		assert.True(t, names["claude-sandbox"], "should list built-in")
		assert.True(t, names["python-ml"], "should list user template")
	})

	// ── Step 5: Create project from claude-sandbox template ──
	t.Run("create_project_from_template", func(t *testing.T) {
		body := `{
			"name": "my-sandbox-project",
			"repo": "org/my-sandbox-project",
			"template": "claude-sandbox"
		}`
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusCreated, rr.Code)

		var resp projectDetailResponse
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
		assert.Equal(t, "my-sandbox-project", resp.Project.Name)
		assert.Equal(t, "claude-sandbox", resp.Project.Template)
		assert.Equal(t, "sandbox-image:latest", resp.Project.Image, "image inherited from template")
		assert.Equal(t, 2.0, resp.Project.CPULimit, "CPU inherited from template")
		assert.Equal(t, int64(2*1024*1024*1024), resp.Project.MemoryLimit, "memory inherited from template")
	})

	// ── Step 6: Create project from template with overrides ──
	t.Run("create_project_template_with_overrides", func(t *testing.T) {
		body := `{
			"name": "custom-sandbox",
			"repo": "org/custom-sandbox",
			"template": "claude-sandbox",
			"image": "my-custom:v2",
			"cpuLimit": 8
		}`
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusCreated, rr.Code)

		var resp projectDetailResponse
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
		assert.Equal(t, "my-custom:v2", resp.Project.Image, "explicit image overrides template")
		assert.Equal(t, 8.0, resp.Project.CPULimit, "explicit CPU overrides template")
		assert.Equal(t, int64(2*1024*1024*1024), resp.Project.MemoryLimit, "memory falls back to template")
		assert.Equal(t, "claude-sandbox", resp.Project.Template)
	})

	// ── Step 7: Workspaces list shows template field ──
	t.Run("workspaces_show_template", func(t *testing.T) {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/workspaces", nil))
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp workspacesListResponse
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))

		templateWorkspaces := 0
		for _, ws := range resp.Workspaces {
			if ws.Template == "claude-sandbox" {
				templateWorkspaces++
			}
		}
		assert.Equal(t, 2, templateWorkspaces, "both projects created from template should show template field")
	})

	// ── Step 8: Reject project creation with invalid template ──
	t.Run("reject_invalid_template", func(t *testing.T) {
		body := `{"name": "bad-proj", "template": "nonexistent-template"}`
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	// ── Step 9: Cannot delete built-in template ──
	t.Run("cannot_delete_builtin", func(t *testing.T) {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/templates/claude-sandbox", nil))
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	// ── Step 10: Delete user template ──
	t.Run("delete_user_template", func(t *testing.T) {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/templates/python-ml", nil))
		assert.Equal(t, http.StatusOK, rr.Code)

		// Verify it's gone.
		rr = httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/templates/python-ml", nil))
		assert.Equal(t, http.StatusNotFound, rr.Code)

		// List should only have built-in now.
		rr = httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/templates", nil))
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp templatesListResponse
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
		for _, tmpl := range resp.Templates {
			assert.NotEqual(t, "python-ml", tmpl.Name, "deleted template should not appear")
		}
	})

	// ── Step 11: Duplicate template name rejected ──
	t.Run("duplicate_template_rejected", func(t *testing.T) {
		body := `{"name": "test-dup", "image": "img:1"}`
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/templates", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusCreated, rr.Code)

		// Try again — should conflict.
		rr = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPost, "/api/templates", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusConflict, rr.Code)
	})

	// ── Step 12: Template create validation ──
	t.Run("template_create_requires_name_and_image", func(t *testing.T) {
		// Missing name
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/templates", strings.NewReader(`{"image": "img:1"}`))
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)

		// Missing image
		rr = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPost, "/api/templates", strings.NewReader(`{"name": "no-image"}`))
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})
}
