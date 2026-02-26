package hub

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplateStore_ListReturnsBuiltIns(t *testing.T) {
	store, err := NewTemplateStore(t.TempDir())
	require.NoError(t, err)

	templates, err := store.List()
	require.NoError(t, err)
	require.NotEmpty(t, templates)

	found := false
	for _, tmpl := range templates {
		if tmpl.Name == "claude-sandbox" {
			found = true
			assert.True(t, tmpl.BuiltIn)
			assert.Equal(t, "sandbox-image:latest", tmpl.Image)
			assert.Equal(t, 2.0, tmpl.CPUDefault)
			assert.Equal(t, int64(2*1024*1024*1024), tmpl.MemoryDefault)
			assert.Contains(t, tmpl.Tags, "claude")
		}
	}
	assert.True(t, found, "claude-sandbox built-in template not found")
}

func TestTemplateStore_SaveGetRoundTrip(t *testing.T) {
	store, err := NewTemplateStore(t.TempDir())
	require.NoError(t, err)

	tmpl := &Template{
		Name:          "my-custom",
		Description:   "Custom dev environment",
		Image:         "myimage:v1",
		CPUDefault:    4.0,
		MemoryDefault: 4 * 1024 * 1024 * 1024,
		Tags:          []string{"custom"},
		Env:           map[string]string{"FOO": "bar"},
	}

	require.NoError(t, store.Save(tmpl))

	got, err := store.Get("my-custom")
	require.NoError(t, err)
	assert.Equal(t, "my-custom", got.Name)
	assert.Equal(t, "myimage:v1", got.Image)
	assert.Equal(t, 4.0, got.CPUDefault)
	assert.False(t, got.BuiltIn)
	assert.Equal(t, map[string]string{"FOO": "bar"}, got.Env)
	assert.False(t, got.CreatedAt.IsZero())
	assert.False(t, got.UpdatedAt.IsZero())

	// Should appear in list
	templates, err := store.List()
	require.NoError(t, err)
	names := make([]string, len(templates))
	for i, tmpl := range templates {
		names[i] = tmpl.Name
	}
	assert.Contains(t, names, "claude-sandbox")
	assert.Contains(t, names, "my-custom")
}

func TestTemplateStore_BuiltInImmutability(t *testing.T) {
	store, err := NewTemplateStore(t.TempDir())
	require.NoError(t, err)

	// Cannot save over a built-in template.
	err = store.Save(&Template{Name: "claude-sandbox", Image: "hacked:latest"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot modify built-in")

	// Cannot delete a built-in template.
	err = store.Delete("claude-sandbox")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot delete built-in")
}

func TestTemplateStore_DeleteUserTemplate(t *testing.T) {
	store, err := NewTemplateStore(t.TempDir())
	require.NoError(t, err)

	require.NoError(t, store.Save(&Template{Name: "disposable", Image: "img:1"}))

	// Verify it exists.
	_, err = store.Get("disposable")
	require.NoError(t, err)

	// Delete it.
	require.NoError(t, store.Delete("disposable"))

	// Verify it's gone.
	_, err = store.Get("disposable")
	assert.Error(t, err)
}

func TestTemplateStore_DeleteNotFound(t *testing.T) {
	store, err := NewTemplateStore(t.TempDir())
	require.NoError(t, err)

	err = store.Delete("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "template not found")
}

func TestTemplateStore_GetBuiltIn(t *testing.T) {
	store, err := NewTemplateStore(t.TempDir())
	require.NoError(t, err)

	tmpl, err := store.Get("claude-sandbox")
	require.NoError(t, err)
	assert.Equal(t, "claude-sandbox", tmpl.Name)
	assert.True(t, tmpl.BuiltIn)
}

func TestTemplateStore_GetNotFound(t *testing.T) {
	store, err := NewTemplateStore(t.TempDir())
	require.NoError(t, err)

	_, err = store.Get("nonexistent")
	assert.Error(t, err)
}

func TestTemplateStore_ConcurrentAccess(t *testing.T) {
	store, err := NewTemplateStore(t.TempDir())
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			name := fmt.Sprintf("concurrent-%d", idx)
			_ = store.Save(&Template{Name: name, Image: "img:" + name})
			_, _ = store.Get(name)
			_, _ = store.List()
		}(i)
	}
	wg.Wait()
}
