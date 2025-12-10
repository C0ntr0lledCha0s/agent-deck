package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ClaudeProject represents a project entry in Claude's config
type ClaudeProject struct {
	LastSessionId string `json:"lastSessionId"`
}

// ClaudeConfig represents the structure of .claude.json
type ClaudeConfig struct {
	Projects map[string]ClaudeProject `json:"projects"`
}

// GetClaudeConfigDir returns the Claude config directory
// Priority: 1) CLAUDE_CONFIG_DIR env, 2) UserConfig setting, 3) ~/.claude
func GetClaudeConfigDir() string {
	// 1. Check env var (highest priority)
	if envDir := os.Getenv("CLAUDE_CONFIG_DIR"); envDir != "" {
		return expandTilde(envDir)
	}

	// 2. Check user config
	userConfig, _ := LoadUserConfig()
	if userConfig != nil && userConfig.Claude.ConfigDir != "" {
		return expandTilde(userConfig.Claude.ConfigDir)
	}

	// 3. Default to ~/.claude
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// GetClaudeSessionID returns the last session ID for a project path
func GetClaudeSessionID(projectPath string) (string, error) {
	configDir := GetClaudeConfigDir()
	configFile := filepath.Join(configDir, ".claude.json")

	data, err := os.ReadFile(configFile)
	if err != nil {
		return "", fmt.Errorf("failed to read Claude config: %w", err)
	}

	var config ClaudeConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return "", fmt.Errorf("failed to parse Claude config: %w", err)
	}

	// Look up project by path
	if project, ok := config.Projects[projectPath]; ok {
		if project.LastSessionId != "" {
			return project.LastSessionId, nil
		}
	}

	return "", fmt.Errorf("no session found for project: %s", projectPath)
}
