package dns

import (
	"strings"
	"testing"
)

func TestSuffixProgression(t *testing.T) {
	parts := strings.Split("blog.example.com", ".")
	var suffixes []string
	for i := 0; i < len(parts); i++ {
		suffixes = append(suffixes, strings.Join(parts[i:], "."))
	}
	expected := []string{"blog.example.com", "example.com", "com"}
	if len(suffixes) != len(expected) {
		t.Fatalf("got %v suffixes, want %v", suffixes, expected)
	}
	for i, s := range suffixes {
		if s != expected[i] {
			t.Errorf("suffix[%d] = %q, want %q", i, s, expected[i])
		}
	}
}

func TestExtractName(t *testing.T) {
	tests := []struct {
		domain string
		zone   string
		want   string
	}{
		{"blog.example.com", "example.com", "blog"},
		{"example.com", "example.com", "@"},
		{"foo.bar.example.com", "example.com", "foo.bar"},
		{"www.example.co.uk", "example.co.uk", "www"},
		{"example.co.uk", "example.co.uk", "@"},
	}
	for _, tt := range tests {
		got := ExtractName(tt.domain, tt.zone)
		if got != tt.want {
			t.Errorf("ExtractName(%q, %q) = %q, want %q", tt.domain, tt.zone, got, tt.want)
		}
	}
}
