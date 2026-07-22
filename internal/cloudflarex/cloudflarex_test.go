package cloudflarex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

const (
	sampleV4 = "173.245.48.0/20\n103.21.244.0/22\n141.101.64.0/18\n108.162.192.0/18\n190.93.240.0/20\n"
	sampleV6 = "2606:4700::/32\n2400:cb00::/32\n"
)

func fakeFetch(v4, v6 string, v4Err, v6Err error) (FetchFunc, *[]string) {
	calls := &[]string{}
	return func(ctx context.Context, url string) ([]byte, error) {
		*calls = append(*calls, url)
		switch url {
		case V4URL:
			return []byte(v4), v4Err
		case V6URL:
			return []byte(v6), v6Err
		default:
			return nil, fmt.Errorf("unexpected url %q", url)
		}
	}, calls
}

func TestRanges(t *testing.T) {
	tests := []struct {
		name    string
		v4      string
		v6      string
		v4Err   error
		v6Err   error
		want    RangeSet
		wantErr string
	}{
		{
			name: "valid lists are sorted and deduplicated",
			v4:   "173.245.48.0/20\n103.21.244.0/22\n141.101.64.0/18\n103.21.244.0/22\n\n108.162.192.0/18\n190.93.240.0/20\n",
			v6:   sampleV6,
			want: RangeSet{
				V4: []string{
					"103.21.244.0/22",
					"108.162.192.0/18",
					"141.101.64.0/18",
					"173.245.48.0/20",
					"190.93.240.0/20",
				},
				V6: []string{"2400:cb00::/32", "2606:4700::/32"},
			},
		},
		{
			name: "bare addresses become single-address ranges",
			v4:   "198.51.100.7\n103.21.244.0/22\n108.162.192.0/18\n141.101.64.0/18\n",
			v6:   "2001:db8::5\n",
			want: RangeSet{
				V4: []string{
					"103.21.244.0/22",
					"108.162.192.0/18",
					"141.101.64.0/18",
					"198.51.100.7/32",
				},
				V6: []string{"2001:db8::5/128"},
			},
		},
		{
			name: "prefixes are canonicalized to their network address",
			v4:   "10.1.2.3/8\n103.21.244.0/22\n108.162.192.0/18\n141.101.64.0/18\n",
			v6:   sampleV6,
			want: RangeSet{
				V4: []string{
					"10.0.0.0/8",
					"103.21.244.0/22",
					"108.162.192.0/18",
					"141.101.64.0/18",
				},
				V6: []string{"2400:cb00::/32", "2606:4700::/32"},
			},
		},
		{
			name: "carriage returns are tolerated",
			v4:   "173.245.48.0/20\r\n103.21.244.0/22\r\n141.101.64.0/18\r\n108.162.192.0/18\r\n190.93.240.0/20\r\n",
			v6:   "2400:cb00::/32\r\n",
			want: RangeSet{
				V4: []string{
					"103.21.244.0/22",
					"108.162.192.0/18",
					"141.101.64.0/18",
					"173.245.48.0/20",
					"190.93.240.0/20",
				},
				V6: []string{"2400:cb00::/32"},
			},
		},
		{
			name:    "html error page is rejected",
			v4:      "<html><body>error</body></html>\n",
			v6:      sampleV6,
			wantErr: "invalid entry",
		},
		{
			name:    "ipv6 range in ipv4 list is rejected",
			v4:      "2400:cb00::/32\n103.21.244.0/22\n108.162.192.0/18\n141.101.64.0/18\n",
			v6:      sampleV6,
			wantErr: "not an IPv4 range",
		},
		{
			name:    "ipv4 range in ipv6 list is rejected",
			v4:      sampleV4,
			v6:      "103.21.244.0/22\n",
			wantErr: "not an IPv6 range",
		},
		{
			name:    "too few ranges in total is rejected",
			v4:      "103.21.244.0/22\n108.162.192.0/18\n",
			v6:      "2400:cb00::/32\n2606:4700::/32\n",
			wantErr: "need at least 5",
		},
		{
			name:    "empty ipv4 list is rejected",
			v4:      "\n\n",
			v6:      sampleV6,
			wantErr: "IPv4 list is empty",
		},
		{
			name:    "empty ipv6 list is rejected",
			v4:      sampleV4,
			v6:      "",
			wantErr: "IPv6 list is empty",
		},
		{
			name:    "ipv4 fetch failure aborts",
			v4Err:   errors.New("connection refused"),
			v6:      sampleV6,
			wantErr: "fetching Cloudflare IPv4 list",
		},
		{
			name:    "ipv6 fetch failure aborts",
			v4:      sampleV4,
			v6Err:   errors.New("connection refused"),
			wantErr: "fetching Cloudflare IPv6 list",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fetch, _ := fakeFetch(tt.v4, tt.v6, tt.v4Err, tt.v6Err)
			got, err := Ranges(context.Background(), fetch)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Ranges() = %+v, want error containing %q", got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Ranges() error = %q, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Ranges() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Ranges() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRangesFetchesBothListsInOrder(t *testing.T) {
	fetch, calls := fakeFetch(sampleV4, sampleV6, nil, nil)
	if _, err := Ranges(context.Background(), fetch); err != nil {
		t.Fatalf("Ranges() error = %v", err)
	}
	if want := []string{V4URL, V6URL}; !reflect.DeepEqual(*calls, want) {
		t.Errorf("fetched URLs = %v, want %v", *calls, want)
	}
}

func TestRangesStopsAfterIPv4FetchFailure(t *testing.T) {
	fetch, calls := fakeFetch("", sampleV6, errors.New("boom"), nil)
	if _, err := Ranges(context.Background(), fetch); err == nil {
		t.Fatal("Ranges() succeeded, want error")
	}
	if want := []string{V4URL}; !reflect.DeepEqual(*calls, want) {
		t.Errorf("fetched URLs = %v, want %v", *calls, want)
	}
}

func TestRangesRejectsNilArguments(t *testing.T) {
	fetch, _ := fakeFetch(sampleV4, sampleV6, nil, nil)
	if _, err := Ranges(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Errorf("Ranges(nil fetch) error = %v, want nil-fetch error", err)
	}
	var nilCtx context.Context
	if _, err := Ranges(nilCtx, fetch); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Errorf("Ranges(nil ctx) error = %v, want nil-context error", err)
	}
}

func TestRangeSetAll(t *testing.T) {
	set := RangeSet{
		V4: []string{"103.21.244.0/22", "108.162.192.0/18"},
		V6: []string{"2400:cb00::/32"},
	}
	want := []string{"103.21.244.0/22", "108.162.192.0/18", "2400:cb00::/32"}
	if got := set.All(); !reflect.DeepEqual(got, want) {
		t.Errorf("All() = %v, want %v", got, want)
	}
}

func TestFetch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			fmt.Fprint(w, "173.245.48.0/20\n")
		case "/big":
			if _, err := w.Write(bytes.Repeat([]byte("a"), maxListBytes+1)); err != nil {
				t.Errorf("writing oversized body: %v", err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	body, err := Fetch(context.Background(), server.URL+"/ok")
	if err != nil {
		t.Fatalf("Fetch(ok) error = %v", err)
	}
	if got, want := string(body), "173.245.48.0/20\n"; got != want {
		t.Errorf("Fetch(ok) = %q, want %q", got, want)
	}

	if _, err := Fetch(context.Background(), server.URL+"/missing"); err == nil || !strings.Contains(err.Error(), "status") {
		t.Errorf("Fetch(missing) error = %v, want unexpected-status error", err)
	}

	if _, err := Fetch(context.Background(), server.URL+"/big"); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("Fetch(big) error = %v, want too-large error", err)
	}

	var nilCtx context.Context
	if _, err := Fetch(nilCtx, server.URL+"/ok"); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Errorf("Fetch(nil ctx) error = %v, want nil-context error", err)
	}
}
