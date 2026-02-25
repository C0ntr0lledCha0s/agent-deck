package web

import "testing"

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "normal filename", input: "normal.txt", expected: "normal.txt"},
		{name: "path traversal", input: "../../../etc/passwd", expected: "etcpasswd"},
		{name: "empty string", input: "", expected: "unnamed"},
		{name: "whitespace only", input: "   ", expected: "unnamed"},
		{name: "path with slashes", input: "path/to/file.js", expected: "pathtofile.js"},
		{name: "backslash path", input: `path\to\file.js`, expected: "pathtofile.js"},
		{name: "dots only", input: "...", expected: "."},
		{name: "mixed traversal", input: "../../foo/../bar.txt", expected: "foobar.txt"},
		{name: "single dot prefix", input: ".hidden", expected: ".hidden"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeFilename(tc.input)
			if got != tc.expected {
				t.Errorf("sanitizeFilename(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}
