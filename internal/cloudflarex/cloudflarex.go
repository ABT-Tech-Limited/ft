// Package cloudflarex fetches and validates Cloudflare's published IP ranges.
package cloudflarex

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"time"
)

const (
	// V4URL and V6URL are Cloudflare's authoritative range lists.
	V4URL = "https://www.cloudflare.com/ips-v4"
	V6URL = "https://www.cloudflare.com/ips-v6"

	// minTotalRanges guards against truncated lists; Cloudflare publishes
	// about 22 ranges.
	minTotalRanges = 5

	// maxListBytes caps each response body; the real lists are under 1 KiB.
	maxListBytes = 1 << 20

	fetchTimeout = 20 * time.Second
)

// FetchFunc downloads one URL and returns its body.
type FetchFunc func(ctx context.Context, url string) ([]byte, error)

// RangeSet holds validated, canonical, sorted Cloudflare CIDR ranges.
type RangeSet struct {
	V4 []string
	V6 []string
}

// All returns the IPv4 ranges followed by the IPv6 ranges.
func (s RangeSet) All() []string {
	all := make([]string, 0, len(s.V4)+len(s.V6))
	all = append(all, s.V4...)
	return append(all, s.V6...)
}

// Ranges downloads both Cloudflare lists and returns them validated. Any
// error means the caller must not touch the firewall: a partial or corrupt
// list would drop legitimate Cloudflare traffic.
func Ranges(ctx context.Context, fetch FetchFunc) (RangeSet, error) {
	if ctx == nil {
		return RangeSet{}, fmt.Errorf("nil context")
	}
	if fetch == nil {
		return RangeSet{}, fmt.Errorf("nil fetch function")
	}
	v4Body, err := fetch(ctx, V4URL)
	if err != nil {
		return RangeSet{}, fmt.Errorf("fetching Cloudflare IPv4 list: %w", err)
	}
	v6Body, err := fetch(ctx, V6URL)
	if err != nil {
		return RangeSet{}, fmt.Errorf("fetching Cloudflare IPv6 list: %w", err)
	}
	v4, err := parseList(string(v4Body), "IPv4", func(prefix netip.Prefix) bool {
		return prefix.Addr().Is4()
	})
	if err != nil {
		return RangeSet{}, err
	}
	v6, err := parseList(string(v6Body), "IPv6", func(prefix netip.Prefix) bool {
		return prefix.Addr().Is6() && !prefix.Addr().Is4In6()
	})
	if err != nil {
		return RangeSet{}, err
	}
	if total := len(v4) + len(v6); total < minTotalRanges {
		return RangeSet{}, fmt.Errorf(
			"fetched %d Cloudflare ranges in total, need at least %d; refusing to act on a suspiciously small list",
			total, minTotalRanges,
		)
	}
	return RangeSet{V4: v4, V6: v6}, nil
}

func parseList(body, family string, matches func(netip.Prefix) bool) ([]string, error) {
	seen := make(map[netip.Prefix]struct{})
	prefixes := make([]netip.Prefix, 0, 32)
	for line := range strings.SplitSeq(body, "\n") {
		entry := strings.TrimSpace(line)
		if entry == "" {
			continue
		}
		prefix, err := parseEntry(entry)
		if err != nil {
			return nil, fmt.Errorf("invalid entry %q in Cloudflare %s list: %w", entry, family, err)
		}
		if !matches(prefix) {
			return nil, fmt.Errorf("entry %q in Cloudflare %s list is not an %s range", entry, family, family)
		}
		canonical := prefix.Masked()
		if _, duplicate := seen[canonical]; duplicate {
			continue
		}
		seen[canonical] = struct{}{}
		prefixes = append(prefixes, canonical)
	}
	if len(prefixes) == 0 {
		return nil, fmt.Errorf("Cloudflare %s list is empty", family)
	}
	slices.SortFunc(prefixes, func(a, b netip.Prefix) int {
		if c := a.Addr().Compare(b.Addr()); c != 0 {
			return c
		}
		return a.Bits() - b.Bits()
	})
	values := make([]string, len(prefixes))
	for i, prefix := range prefixes {
		values[i] = prefix.String()
	}
	return values, nil
}

func parseEntry(entry string) (netip.Prefix, error) {
	if prefix, err := netip.ParsePrefix(entry); err == nil {
		return prefix, nil
	}
	addr, err := netip.ParseAddr(entry)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

// Fetch is the default FetchFunc: a plain GET with a timeout, a success-only
// status check and a response size cap.
func Fetch(ctx context.Context, url string) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("nil context")
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %s from %s", response.Status, url)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxListBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", url, err)
	}
	if len(body) > maxListBytes {
		return nil, fmt.Errorf("response from %s is too large (over %d bytes)", url, maxListBytes)
	}
	return body, nil
}
