package dns

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"
)

// DefaultHTTPClient returns an *http.Client with a 10-second timeout
// suitable for DNS provider API calls.
func DefaultHTTPClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second}
}

// Provider defines the interface for DNS provider implementations.
type Provider interface {
	Name() string
	EnsureRecord(ctx context.Context, zone, name, recordType, value string, ttl int) (string, bool, error)
	DeleteRecord(ctx context.Context, zone, recordID string) error
	ListRecords(ctx context.Context, zone string) ([]Record, error)
}

// Record represents a DNS record.
type Record struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
	TTL   int    `json:"ttl"`
}

// Config holds authentication configuration for a DNS provider.
type Config struct {
	Token  string
	Secret string // for providers that need two tokens (Porkbun)
	APIURL string // optional override
}

var registry = make(map[string]func(Config) (Provider, error))

// Register adds a provider factory to the global registry.
func Register(name string, factory func(Config) (Provider, error)) {
	registry[name] = factory
}

// Get returns a provider instance by name with the given config.
func Get(name string, cfg Config) (Provider, error) {
	fn, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown DNS provider: %s", name)
	}
	return fn(cfg)
}

// Providers returns a sorted list of registered provider names.
func Providers() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
