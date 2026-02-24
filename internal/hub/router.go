package hub

import (
	"strings"
)

// Route matches a natural language message against project keywords.
// Returns the best-matching project with confidence score, or nil if no keywords match.
// Confidence = matched keywords / total keywords for the winning project.
func Route(message string, projects []*Project) *RouteResult {
	if message == "" || len(projects) == 0 {
		return nil
	}

	words := strings.Fields(strings.ToLower(message))

	var bestProject string
	var bestCount int
	var bestTotal int
	var bestKeywords []string

	for _, p := range projects {
		if len(p.Keywords) == 0 {
			continue
		}

		var matched []string
		for _, kw := range p.Keywords {
			kwLower := strings.ToLower(kw)
			for _, w := range words {
				if w == kwLower || strings.Contains(w, kwLower) {
					matched = append(matched, kw)
					break
				}
			}
		}

		if len(matched) > bestCount {
			bestProject = p.Name
			bestCount = len(matched)
			bestTotal = len(p.Keywords)
			bestKeywords = matched
		}
	}

	if bestCount == 0 {
		return nil
	}

	return &RouteResult{
		Project:         bestProject,
		Confidence:      float64(bestCount) / float64(bestTotal),
		MatchedKeywords: bestKeywords,
	}
}
