package hub

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// builtInTemplates defines hardcoded templates that ship with Agent Deck.
var builtInTemplates = []*Template{{
	Name:          "claude-sandbox",
	Description:   "Sandboxed Claude Code environment with Node.js 22, Python, gh CLI, and firewall isolation",
	Image:         "sandbox-image:latest",
	CPUDefault:    2.0,
	MemoryDefault: 2 * 1024 * 1024 * 1024, // 2GB
	Tags:          []string{"claude", "sandbox", "docker", "ai"},
	BuiltIn:       true,
}}

// TemplateStore provides filesystem JSON-based CRUD for Template records.
// Built-in templates are merged with user-defined templates stored as
// individual JSON files under basePath/templates/.
type TemplateStore struct {
	mu          sync.RWMutex
	templateDir string
}

// NewTemplateStore creates a TemplateStore backed by the given base directory.
// It creates the templates/ subdirectory if it does not exist.
func NewTemplateStore(basePath string) (*TemplateStore, error) {
	templateDir := filepath.Join(basePath, "templates")
	if err := os.MkdirAll(templateDir, 0o755); err != nil {
		return nil, fmt.Errorf("create template directory: %w", err)
	}
	return &TemplateStore{templateDir: templateDir}, nil
}

// List returns all templates (built-in + user) sorted by name.
func (s *TemplateStore) List() ([]*Template, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Start with copies of built-in templates.
	templates := make([]*Template, 0, len(builtInTemplates))
	for _, bi := range builtInTemplates {
		cp := *bi
		templates = append(templates, &cp)
	}

	// Merge user-defined templates from filesystem.
	entries, err := os.ReadDir(s.templateDir)
	if err != nil {
		return templates, nil // return built-ins on read error
	}

	builtInNames := make(map[string]bool, len(builtInTemplates))
	for _, bi := range builtInTemplates {
		builtInNames[bi.Name] = true
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		tmpl, err := s.readTemplateFile(entry.Name())
		if err != nil {
			continue // skip corrupt files
		}
		// Skip user files that shadow built-in names.
		if builtInNames[tmpl.Name] {
			continue
		}
		templates = append(templates, tmpl)
	}

	sort.Slice(templates, func(i, j int) bool {
		return templates[i].Name < templates[j].Name
	})

	return templates, nil
}

// Get retrieves a single template by name, checking built-ins first.
func (s *TemplateStore) Get(name string) (*Template, error) {
	if !validProjectName(name) {
		return nil, fmt.Errorf("invalid template name: %q", name)
	}

	// Check built-ins first.
	for _, bi := range builtInTemplates {
		if bi.Name == name {
			cp := *bi
			return &cp, nil
		}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.readTemplateFile(name + ".json")
}

// Save persists a user-defined template. Built-in templates cannot be saved.
func (s *TemplateStore) Save(tmpl *Template) error {
	if !validProjectName(tmpl.Name) {
		return fmt.Errorf("invalid template name: %q", tmpl.Name)
	}

	// Reject saving built-in templates.
	for _, bi := range builtInTemplates {
		if bi.Name == tmpl.Name {
			return fmt.Errorf("cannot modify built-in template: %s", tmpl.Name)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tmpl.BuiltIn = false
	if tmpl.CreatedAt.IsZero() {
		tmpl.CreatedAt = time.Now().UTC()
	}
	tmpl.UpdatedAt = time.Now().UTC()

	data, err := json.MarshalIndent(tmpl, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal template: %w", err)
	}

	path := filepath.Join(s.templateDir, tmpl.Name+".json")
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write template file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename template file: %w", err)
	}

	return nil
}

// Delete removes a user-defined template. Built-in templates cannot be deleted.
func (s *TemplateStore) Delete(name string) error {
	if !validProjectName(name) {
		return fmt.Errorf("invalid template name: %q", name)
	}

	// Reject deleting built-in templates.
	for _, bi := range builtInTemplates {
		if bi.Name == name {
			return fmt.Errorf("cannot delete built-in template: %s", name)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(s.templateDir, name+".json")
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("template not found: %s", name)
		}
		return fmt.Errorf("delete template file: %w", err)
	}
	return nil
}

func (s *TemplateStore) readTemplateFile(filename string) (*Template, error) {
	data, err := os.ReadFile(filepath.Join(s.templateDir, filename))
	if err != nil {
		return nil, fmt.Errorf("read template file %s: %w", filename, err)
	}
	var tmpl Template
	if err := json.Unmarshal(data, &tmpl); err != nil {
		return nil, fmt.Errorf("unmarshal template %s: %w", filename, err)
	}
	return &tmpl, nil
}
