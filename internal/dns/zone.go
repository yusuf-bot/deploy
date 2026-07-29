package dns

import (
	"context"
	"fmt"
	"strings"
)

// ExtractZone tries progressively shorter domain suffixes against the
// provider's zone list to find the actual DNS zone.
// For "blog.example.com" it tries "blog.example.com", then "example.com".
// Returns the first suffix that the provider accepts as a valid zone.
func ExtractZone(ctx context.Context, p Provider, domain string) (string, error) {
	parts := strings.Split(domain, ".")
	for i := 0; i < len(parts); i++ {
		candidate := strings.Join(parts[i:], ".")
		_, err := p.ListRecords(ctx, candidate)
		if err == nil {
			return candidate, nil
		}
		// If error was something transient/network, we could break,
		// but zone-not-found errors are provider-specific.
		// Continue trying shorter suffixes.
	}
	return "", fmt.Errorf("no zone found for %q", domain)
}

// ExtractName returns the record name (subdomain part) given a full domain
// and its zone. For "blog.example.com" with zone "example.com", returns "blog".
// If domain == zone, returns "@" (root record).
func ExtractName(domain, zone string) string {
	if domain == zone {
		return "@"
	}
	return strings.TrimSuffix(domain, "."+zone)
}
