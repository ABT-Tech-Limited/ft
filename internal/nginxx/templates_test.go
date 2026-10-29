package nginxx

import (
	"bytes"
	"net/netip"
	"os"
	"strings"
	"testing"

	"github.com/ABT-Tech-Limited/ft/internal/cloudflarex"
)

func templateRanges() cloudflarex.RangeSet {
	return cloudflarex.RangeSet{
		V4: []string{
			netip.PrefixFrom(netip.AddrFrom4([4]byte{173, 245, 48, 0}), 20).String(),
			netip.PrefixFrom(netip.AddrFrom4([4]byte{103, 21, 244, 0}), 22).String(),
			netip.PrefixFrom(netip.AddrFrom4([4]byte{103, 22, 200, 0}), 22).String(),
		},
		V6: []string{
			netip.PrefixFrom(netip.AddrFrom16([16]byte{0x24, 0x00, 0xcb, 0x00}), 32).String(),
			netip.PrefixFrom(netip.AddrFrom16([16]byte{0x26, 0x06, 0x47, 0x00}), 32).String(),
		},
	}
}

func readDefault(t *testing.T, version string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/ubuntu-" + version + ".conf")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestOptimizeMainRecognizedDefaults(t *testing.T) {
	for _, version := range []string{"24.04", "26.04"} {
		t.Run(version, func(t *testing.T) {
			original := readDefault(t, version)
			optimized, err := OptimizeMain(original)
			if err != nil {
				t.Fatalf("OptimizeMain() error = %v", err)
			}
			if !bytes.HasPrefix(optimized, []byte(ManagedHeader)) {
				t.Errorf("optimized configuration has no ownership marker")
			}
			for _, want := range []string{
				"worker_processes auto;", "worker_connections 2048;", "server_tokens off;",
				"sendfile on;", "tcp_nopush on;", "gzip on;",
				"include /etc/nginx/conf.d/*.conf;", "include /etc/nginx/sites-enabled/*;",
			} {
				if !bytes.Contains(optimized, []byte(want)) {
					t.Errorf("optimized configuration does not contain %q", want)
				}
			}
			for _, line := range strings.Split(string(original), "\n") {
				if strings.Contains(line, "ssl_protocols ") || strings.Contains(line, "ssl_prefer_server_ciphers ") || strings.Contains(line, "worker_cpu_affinity ") {
					if !bytes.Contains(optimized, []byte(line)) {
						t.Errorf("optimization changed unrelated directive %q", line)
					}
				}
			}
			again, err := OptimizeMain(optimized)
			if err != nil || !bytes.Equal(again, optimized) {
				t.Errorf("optimization is not idempotent: error=%v", err)
			}
			if bytes.Contains(optimized, []byte("client_max_body_size")) || bytes.Contains(optimized, []byte("gzip_comp_level 4;")) {
				t.Error("main config duplicates directives owned by HTTPConfig")
			}
		})
	}
}

func TestOptimizeMainAcceptsHarmlessFormatting(t *testing.T) {
	original := string(readDefault(t, "24.04"))
	modified := "# Local comment, no directive changes.\r\n" + strings.ReplaceAll(original, "\n", "\r\n")
	modified = strings.ReplaceAll(modified, "\t", "    ")
	if _, err := OptimizeMain([]byte(modified)); err != nil {
		t.Fatalf("OptimizeMain() rejected comments/whitespace: %v", err)
	}
}

func TestOptimizeMainRejectsCustomConfig(t *testing.T) {
	original := string(readDefault(t, "24.04"))
	tests := []struct {
		name string
		data string
	}{
		{"empty", ""},
		{"different worker count", strings.Replace(original, "worker_processes auto;", "worker_processes 2;", 1)},
		{"different capacity", strings.Replace(original, "worker_connections 768;", "worker_connections 8192;", 1)},
		{"custom include", original + "\ninclude /etc/custom.conf;\n"},
		{"marker does not grant ownership", ManagedHeader + "\nhttp { server { listen 80; } }\n"},
		{"quote contains comment", strings.Replace(original, "user www-data;", "user \"www-data; #\";", 1)},
		{"escape contains semicolon", strings.Replace(original, "user www-data;", "user www-data\\;;", 1)},
		{"unterminated quote", original + "\ninclude \"broken;\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if output, err := OptimizeMain([]byte(tt.data)); err == nil || output != nil {
				t.Fatalf("OptimizeMain() = %q, %v; want nil output and refusal", output, err)
			}
		})
	}
}

func TestHTTPConfigTrustAndDefaults(t *testing.T) {
	config := string(HTTPConfig())
	for _, want := range []string{
		ManagedHeader,
		"include " + ActivePath + ";",
		"real_ip_header CF-Connecting-IP;",
		"client_max_body_size 10m;", "proxy_read_timeout 60s;", "proxy_send_timeout 60s;",
		"gzip_vary on;", "gzip_min_length 1024;", "gzip_comp_level 4;",
		"gzip_types text/css application/javascript text/javascript image/svg+xml;",
		`map "$ft_from_cloudflare:$http_x_forwarded_proto" $ft_forwarded_proto`,
		"default   $scheme;", `"1:http"  http;`, `"1:https" https;`,
		"map $http_upgrade $ft_connection_upgrade", `""      "";`,
	} {
		if !strings.Contains(config, want) {
			t.Errorf("HTTPConfig() missing %q", want)
		}
	}
	for _, unwanted := range []string{
		"gzip on;", "server_tokens ", "worker_connections ", "ssl_protocols ",
		"application/json", "proxy_cache ", "limit_req ", "Strict-Transport-Security", "return 301", "return 308",
	} {
		if strings.Contains(config, unwanted) {
			t.Errorf("HTTPConfig() contains unwanted directive/type %q", unwanted)
		}
	}
}

func TestProxySnippetNormalizesForwarding(t *testing.T) {
	config := string(ProxySnippet())
	for _, want := range []string{
		ManagedHeader, "proxy_http_version 1.1;",
		"proxy_set_header Host              $host;",
		"proxy_set_header X-Real-IP         $remote_addr;",
		"proxy_set_header X-Forwarded-For   $remote_addr;",
		"proxy_set_header X-Forwarded-Host  $host;",
		"proxy_set_header X-Forwarded-Proto $ft_forwarded_proto;",
		"proxy_set_header Upgrade          $http_upgrade;",
		"proxy_set_header Connection       $ft_connection_upgrade;",
		`proxy_set_header Forwarded        "";`,
		`proxy_set_header X-Forwarded-Port "";`,
		`proxy_set_header CF-Connecting-IP "";`,
	} {
		if !strings.Contains(config, want) {
			t.Errorf("ProxySnippet() missing %q", want)
		}
	}
	for _, unwanted := range []string{"$proxy_add_x_forwarded_for", "proxy_read_timeout", "proxy_send_timeout", "proxy_buffering off", "proxy_cache"} {
		if strings.Contains(config, unwanted) {
			t.Errorf("ProxySnippet() contains unwanted %q", unwanted)
		}
	}
}

func TestCloudflareFilesCanonicalAndConsistent(t *testing.T) {
	ranges := templateRanges()
	baseRealIP, baseGeo, err := CloudflareFiles(ranges)
	if err != nil {
		t.Fatal(err)
	}
	for _, prefix := range ranges.All() {
		if !bytes.Contains(baseRealIP, []byte("set_real_ip_from "+prefix+";\n")) || !bytes.Contains(baseGeo, []byte(prefix+" 1;\n")) {
			t.Errorf("both files must contain %q", prefix)
		}
	}
	ranges.V4[0], ranges.V4[2] = ranges.V4[2], ranges.V4[0]
	ranges.V6[0], ranges.V6[1] = ranges.V6[1], ranges.V6[0]
	ranges.V4 = append(ranges.V4, ranges.V4[0])
	realIP, geo, err := CloudflareFiles(ranges)
	if err != nil || !bytes.Equal(realIP, baseRealIP) || !bytes.Equal(geo, baseGeo) {
		t.Fatalf("reordering/duplicates changed output: %v", err)
	}
	for _, generated := range [][]byte{realIP, geo} {
		if !bytes.HasPrefix(generated, []byte(ManagedHeader)) {
			t.Error("missing managed marker")
		}
	}
}

func TestCloudflareFilesRejectsUnsafeRanges(t *testing.T) {
	tests := []struct {
		name   string
		change func(*cloudflarex.RangeSet)
	}{
		{"empty IPv4", func(r *cloudflarex.RangeSet) { r.V4 = nil }},
		{"empty IPv6", func(r *cloudflarex.RangeSet) { r.V6 = nil }},
		{"truncated", func(r *cloudflarex.RangeSet) { r.V4 = r.V4[:1]; r.V6 = r.V6[:1] }},
		{"IPv4 default route", func(r *cloudflarex.RangeSet) { r.V4[0] = "0.0.0.0/0" }},
		{"IPv6 default route", func(r *cloudflarex.RangeSet) { r.V6[0] = "::/0" }},
		{"private IPv4", func(r *cloudflarex.RangeSet) { r.V4[0] = "10.0.0.0/16" }},
		{"loopback", func(r *cloudflarex.RangeSet) { r.V4[0] = "127.0.0.1/32" }},
		{"link local", func(r *cloudflarex.RangeSet) { r.V4[0] = "169.254.0.0/16" }},
		{"multicast", func(r *cloudflarex.RangeSet) { r.V4[0] = "224.0.0.0/16" }},
		{"private IPv6", func(r *cloudflarex.RangeSet) { r.V6[0] = "fc00::/32" }},
		{"documentation IPv4", func(r *cloudflarex.RangeSet) { r.V4[0] = "192.0.2.0/24" }},
		{"documentation IPv6", func(r *cloudflarex.RangeSet) { r.V6[0] = "2001:db8::/32" }},
		{"benchmark IPv6", func(r *cloudflarex.RangeSet) {
			r.V6[0] = netip.PrefixFrom(netip.AddrFrom16([16]byte{0x20, 0x01, 0x00, 0x02}), 48).String()
		}},
		{"ORCHID IPv6", func(r *cloudflarex.RangeSet) {
			r.V6[0] = netip.PrefixFrom(netip.AddrFrom16([16]byte{0x20, 0x01, 0x00, 0x10}), 28).String()
		}},
		{"ORCHIDv2 IPv6", func(r *cloudflarex.RangeSet) {
			r.V6[0] = netip.PrefixFrom(netip.AddrFrom16([16]byte{0x20, 0x01, 0x00, 0x20}), 28).String()
		}},
		{"too broad", func(r *cloudflarex.RangeSet) { r.V4[0] = "8.0.0.0/8" }},
		{"IPv6 too broad", func(r *cloudflarex.RangeSet) { r.V6[0] = "2000::/3" }},
		{"wrong family", func(r *cloudflarex.RangeSet) { r.V4[0] = r.V6[0] }},
		{"mapped IPv6", func(r *cloudflarex.RangeSet) { r.V6[0] = "::ffff:192.0.2.1/128" }},
		{"config injection", func(r *cloudflarex.RangeSet) { r.V4[0] = "0.0.0.0/0; include /tmp/evil;" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ranges := templateRanges()
			tt.change(&ranges)
			realIP, geo, err := CloudflareFiles(ranges)
			if err == nil || realIP != nil || geo != nil {
				t.Fatalf("CloudflareFiles() = %q, %q, %v; want refusal", realIP, geo, err)
			}
		})
	}
}

func TestExamples(t *testing.T) {
	goExample, err := Example("go")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"upstream ", "keepalive 16;", "listen 80;", "include " + ProxyPath + ";", "proxy_read_timeout 300s;", "heartbeat", "proxy_pass http://"} {
		if !strings.Contains(goExample, want) {
			t.Errorf("Go example missing %q", want)
		}
	}
	staticExample, err := Example("static")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"listen 80;", "expires -1;", "expires 1h;", "try_files $uri =404;", "try_files $uri $uri/ =404;", "SPA", "fingerprint"} {
		if !strings.Contains(staticExample, want) {
			t.Errorf("static example missing %q", want)
		}
	}
	for _, unwanted := range []string{"add_header Cache-Control", "expires 1y;", "include " + ProxyPath, "proxy_pass", "return 301", "return 308"} {
		if strings.Contains(staticExample, unwanted) {
			t.Errorf("static example contains unsafe/duplicate %q", unwanted)
		}
	}
	both, err := Example("")
	if err != nil || !strings.Contains(both, goExample) || !strings.Contains(both, staticExample) {
		t.Errorf("combined example omitted a template: %v", err)
	}
	if result, err := Example("php"); err == nil || result != "" {
		t.Errorf("Example(php) = %q, %v; want refusal", result, err)
	}
}
