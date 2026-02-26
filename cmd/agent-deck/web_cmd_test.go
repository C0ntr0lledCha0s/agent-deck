package main

import "testing"

func TestHasHeadlessFlag(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{"no flags", []string{}, false},
		{"other flags only", []string{"--listen", "0.0.0.0:9000", "--read-only"}, false},
		{"headless present", []string{"--headless"}, true},
		{"headless with other flags", []string{"--listen", "0.0.0.0:9000", "--headless"}, true},
		{"headless first", []string{"--headless", "--listen", "0.0.0.0:9000"}, true},
		{"single dash variant", []string{"-headless"}, true},
		{"after double dash terminator", []string{"--", "--headless"}, false},
		{"before double dash terminator", []string{"--headless", "--"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasHeadlessFlag(tt.args)
			if got != tt.want {
				t.Errorf("hasHeadlessFlag(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}
