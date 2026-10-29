// Package nginxx manages conservative Nginx defaults and Cloudflare trust data.
package nginxx

import (
	_ "embed"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/ABT-Tech-Limited/ft/internal/cloudflarex"
)

// Managed paths are deliberately separate from user-owned virtual hosts.
const (
	MainPath      = "/etc/nginx/nginx.conf"
	HTTPPath      = "/etc/nginx/conf.d/ft-http.conf"
	ProxyPath     = "/etc/nginx/snippets/ft-go-proxy.conf"
	ManagedDir    = "/etc/nginx/ft"
	ActivePath    = ManagedDir + "/cloudflare-active.conf"
	CronPath      = "/etc/cron.d/ft-nginx-cloudflare"
	ManagedHeader = "# Managed by ft."
)

// These package defaults come from Ubuntu's nginx source package:
// https://git.launchpad.net/ubuntu/+source/nginx/plain/debian/conf/nginx.conf?h=ubuntu/noble-updates
// https://git.launchpad.net/ubuntu/+source/nginx/plain/debian/conf/nginx.conf?h=ubuntu/resolute-updates
//
//go:embed testdata/ubuntu-24.04.conf
var ubuntu2404Default string

//go:embed testdata/ubuntu-26.04.conf
var ubuntu2604Default string

// OptimizeMain accepts only the known Ubuntu defaults or the equivalent
// ft-managed baseline. Comments and whitespace do not grant new ownership.
func OptimizeMain(content []byte) ([]byte, error) {
	if len(content) > 1<<20 {
		return nil, fmt.Errorf("refusing an unexpectedly large nginx.conf")
	}
	actual, err := configTokens(string(content))
	if err != nil {
		return nil, fmt.Errorf("refusing to modify invalid nginx.conf: %w", err)
	}
	for _, original := range []string{ubuntu2404Default, ubuntu2604Default} {
		managed := strings.Replace(original, "worker_connections 768;", "worker_connections 2048;", 1)
		managed = strings.Replace(managed, "# server_tokens off;", "server_tokens off;", 1)
		managed = strings.Replace(managed, "server_tokens build;", "server_tokens off;", 1)
		managed = ManagedHeader + " Conservative Ubuntu baseline; edit site files separately.\n" + managed
		for _, candidate := range []string{original, managed} {
			known, err := configTokens(candidate)
			if err != nil {
				return nil, fmt.Errorf("invalid embedded nginx template: %w", err)
			}
			if slices.Equal(actual, known) {
				return []byte(managed), nil
			}
		}
	}
	return nil, fmt.Errorf("refusing to overwrite custom nginx.conf; expected Ubuntu 24.04/26.04 defaults or the unchanged ft-managed baseline")
}

// configTokens compares known configurations without stripping comments from
// quoted or escaped tokens. This is recognition, not a general Nginx rewriter.
func configTokens(content string) ([]string, error) {
	var tokens []string
	var token strings.Builder
	var quote byte
	flush := func() {
		if token.Len() > 0 {
			tokens = append(tokens, token.String())
			token.Reset()
		}
	}
	for i := 0; i < len(content); i++ {
		ch := content[i]
		if ch == '\\' {
			if i+1 == len(content) {
				return nil, fmt.Errorf("dangling escape")
			}
			token.WriteByte(ch)
			i++
			token.WriteByte(content[i])
			continue
		}
		if quote != 0 {
			token.WriteByte(ch)
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			token.WriteByte(ch)
			continue
		}
		if ch == '#' && token.Len() == 0 {
			for i < len(content) && content[i] != '\n' {
				i++
			}
			continue
		}
		switch ch {
		case ' ', '\t', '\r', '\n':
			flush()
		case '{', '}', ';':
			flush()
			tokens = append(tokens, string(ch))
		default:
			token.WriteByte(ch)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quoted token")
	}
	flush()
	return tokens, nil
}

// HTTPConfig returns the single http-context include; it never declares a site.
func HTTPConfig() []byte {
	return []byte(ManagedHeader + `
# Loaded once inside http {}; do not include this file in server/location.
client_max_body_size 10m;
proxy_read_timeout 60s;
proxy_send_timeout 60s;

gzip_vary on;
gzip_min_length 1024;
gzip_comp_level 4;
gzip_types text/css application/javascript text/javascript image/svg+xml;

include ` + ActivePath + `;
real_ip_header CF-Connecting-IP;

map "$ft_from_cloudflare:$http_x_forwarded_proto" $ft_forwarded_proto {
    default   $scheme;
    "1:http"  http;
    "1:https" https;
}

map $http_upgrade $ft_connection_upgrade {
    default upgrade;
    ""      "";
}
`)
}

// ProxySnippet returns a location-context include for Go HTTP and WebSocket
// proxies. Read/send timeouts inherit from http and can be overridden locally.
func ProxySnippet() []byte {
	return []byte(ManagedHeader + `
# Include in each proxy_pass location; merge existing proxy headers first.
proxy_http_version 1.1;
proxy_set_header Host              $host;
proxy_set_header X-Real-IP         $remote_addr;
proxy_set_header X-Forwarded-For   $remote_addr;
proxy_set_header X-Forwarded-Host  $host;
proxy_set_header X-Forwarded-Proto $ft_forwarded_proto;
proxy_set_header Upgrade          $http_upgrade;
proxy_set_header Connection       $ft_connection_upgrade;

# Backends should trust only these normalized headers from Nginx.
proxy_set_header Forwarded        "";
proxy_set_header X-Forwarded-Port "";
proxy_set_header CF-Connecting-IP "";
`)
}

var nonPublicRanges = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2001:2::/48"),
	netip.MustParsePrefix("2001:10::/28"),
	netip.MustParsePrefix("2001:20::/28"),
}

// CloudflareFiles renders both trust views from one validated, sorted snapshot.
// A suspicious response must not expand the addresses trusted to supply headers.
func CloudflareFiles(ranges cloudflarex.RangeSet) (realIP []byte, geo []byte, err error) {
	if len(ranges.V4) == 0 || len(ranges.V6) == 0 {
		return nil, nil, fmt.Errorf("Cloudflare trust requires nonempty IPv4 and IPv6 lists")
	}
	if len(ranges.V4)+len(ranges.V6) > 1024 {
		return nil, nil, fmt.Errorf("unexpectedly large Cloudflare trust list")
	}
	unique := make(map[netip.Prefix]struct{})
	for family, entries := range [][]string{ranges.V4, ranges.V6} {
		for _, entry := range entries {
			prefix, err := netip.ParsePrefix(entry)
			if err != nil {
				return nil, nil, fmt.Errorf("invalid Cloudflare CIDR %q: %w", entry, err)
			}
			prefix = prefix.Masked()
			addr := prefix.Addr()
			minimumBits := 12
			if family == 1 {
				minimumBits = 24
			}
			if addr.Is4() != (family == 0) || addr.Is4In6() || prefix.Bits() < minimumBits ||
				!addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() {
				return nil, nil, fmt.Errorf("unsafe Cloudflare trust range %q", entry)
			}
			if addr.Is6() && !netip.MustParsePrefix("2000::/3").Contains(addr) {
				return nil, nil, fmt.Errorf("non-public Cloudflare IPv6 range %q", entry)
			}
			for _, reserved := range nonPublicRanges {
				if prefix.Overlaps(reserved) {
					return nil, nil, fmt.Errorf("Cloudflare trust range %q overlaps reserved space", entry)
				}
			}
			unique[prefix] = struct{}{}
		}
	}
	if len(unique) < 5 {
		return nil, nil, fmt.Errorf("suspiciously small Cloudflare trust list: %d distinct ranges", len(unique))
	}
	prefixes := make([]netip.Prefix, 0, len(unique))
	for prefix := range unique {
		prefixes = append(prefixes, prefix)
	}
	slices.SortFunc(prefixes, func(a, b netip.Prefix) int {
		if cmp := a.Addr().Compare(b.Addr()); cmp != 0 {
			return cmp
		}
		return a.Bits() - b.Bits()
	})
	var realIPText, geoText strings.Builder
	realIPText.WriteString(ManagedHeader + "\n")
	geoText.WriteString(ManagedHeader + "\n")
	for _, prefix := range prefixes {
		fmt.Fprintf(&realIPText, "set_real_ip_from %s;\n", prefix)
		fmt.Fprintf(&geoText, "%s 1;\n", prefix)
	}
	return []byte(realIPText.String()), []byte(geoText.String()), nil
}

// Example returns a read-only, commented Nginx example for go, static, or both.
// The output is never installed or enabled as a virtual host automatically.
func Example(kind string) (string, error) {
	goConfig := `# Go HTTP API and optional WebSocket example.
# Run ft install nginx first to create the referenced managed snippet.
# Replace the domain, upstream name, port and WebSocket path for your service.
# Keep the Go listener private; trust proxy headers only from Nginx.
# Merge/remove overlapping proxy_http_version and proxy_set_header directives
# before adding the snippet to existing locations.
upstream ft_example_go_backend {
    server 127.0.0.1:8080;
    keepalive 16;
}

server {
    listen 80;
    server_name api.example.com;

    location / {
        include ` + ProxyPath + `;
        proxy_pass http://ft_example_go_backend;
    }

    # Optional: replace /ws with your actual WebSocket endpoint or remove this
    # block. 300s is an idle timeout, not a maximum connection lifetime.
    # Send regular server heartbeat frames (for example every 30s).
    location = /ws {
        include ` + ProxyPath + `;
        proxy_pass http://ft_example_go_backend;
        proxy_read_timeout 300s;
    }
}
`
	staticConfig := `# Static website example. Replace the domain and document root.
# No proxy snippet is needed. This is HTTP-compatible with Cloudflare Flexible;
# it does not configure certificates or redirect origin HTTP to HTTPS.
server {
    listen 80;
    server_name www.example.com;
    root /var/www/example;
    index index.html;

    # HTML and unclassified files must be revalidated before reuse.
    expires -1;

    location / {
        try_files $uri $uri/ =404;
        # For an SPA only, replace the final =404 with /index.html.
    }

    location ~ /\.(?!well-known(?:/|$)) {
        deny all;
    }

    # Cache existing public static assets for one hour, not missing resources.
    # expires supplies Cache-Control; do not add a duplicate header here.
    # Longer immutable caching is opt-in only for fingerprinted filenames.
    location ~* \.(?:css|js|mjs|png|jpe?g|gif|webp|avif|svg|ico|woff2?|ttf|eot)$ {
        try_files $uri =404;
        expires 1h;
    }
}
`
	switch kind {
	case "":
		return goConfig + "\n" + staticConfig, nil
	case "go":
		return goConfig, nil
	case "static":
		return staticConfig, nil
	default:
		return "", fmt.Errorf("unknown Nginx example %q; use go or static", kind)
	}
}
