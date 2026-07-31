// Package caddyfile manages Caddyfile configuration for the deploy daemon.
//
// Caddy runs as a subprocess managed by the daemon (not embedded).
// The daemon writes Caddyfile snippets in ~/.deploy/caddy/sites/*.conf.
// The main Caddyfile imports all snippets via a glob pattern.
// On changes: write/update/delete snippet files, SIGHUP the Caddy process.
package caddyfile

import (
	"fmt"
	"strings"
)

// MainCaddyfile returns the static main Caddyfile content.
// It disables the admin API and imports all site snippets.
func MainCaddyfile() string {
	return `{
    admin off
    auto_https off
    servers {
        protocols h1 h2
    }
}

# Site snippets
import sites/*.conf
`
}

// SiteBlock generates a Caddyfile site block for a domain proxying to localhost:port.
// Localhost domains (*.localhost) get tls internal for local HTTPS.
func SiteBlock(domain string, port int) string {
	if port <= 0 || port > 65535 {
		return fmt.Sprintf("# invalid port %d for domain %s\n", port, domain)
	}

	var b strings.Builder
	b.WriteString(domain)
	b.WriteString(" {\n")

	if IsLocalDomain(domain) {
		b.WriteString("    tls internal\n")
	}

	fmt.Fprintf(&b, "    reverse_proxy localhost:%d\n", port)
	b.WriteString("}\n")

	return b.String()
}

// IsLocalDomain returns true if domain is a *.localhost domain.
func IsLocalDomain(domain string) bool {
	return strings.HasSuffix(domain, ".localhost") || domain == "localhost"
}

// SiteFilename returns the filesystem-friendly filename for a domain snippet.
// Format: <app>-<random>.conf
// The app name is sanitised and a short random suffix prevents collisions.
func SiteFilename(appName string, domain string) string {
	// Sanitise app name for filesystem use (remove chars that are problematic)
	sanitised := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, appName)

	// Use domain as-is but replace dots with dashes for the filename
	domainPart := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, strings.Split(domain, ":")[0]) // strip port if present

	return fmt.Sprintf("%s-%s.conf", sanitised, domainPart)
}
