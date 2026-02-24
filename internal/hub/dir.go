package hub

import (
	"path/filepath"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// GetHubDir returns the hub data directory for the given profile.
// The path is <profileDir>/hub, e.g. ~/.agent-deck/profiles/default/hub.
func GetHubDir(profile string) (string, error) {
	profileDir, err := session.GetProfileDir(profile)
	if err != nil {
		return "", err
	}
	return filepath.Join(profileDir, "hub"), nil
}
