package osrelease

import (
	"strings"
	"testing"
)

const ubuntu2404 = `PRETTY_NAME="Ubuntu 24.04.2 LTS"
NAME="Ubuntu"
VERSION_ID="24.04"
VERSION="24.04.2 LTS (Noble Numbat)"
VERSION_CODENAME=noble
ID=ubuntu
ID_LIKE=debian
HOME_URL="https://www.ubuntu.com/"
UBUNTU_CODENAME=noble
`

const debian12 = `PRETTY_NAME="Debian GNU/Linux 12 (bookworm)"
NAME="Debian GNU/Linux"
VERSION_ID="12"
VERSION_CODENAME=bookworm
ID=debian
`

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Info
	}{
		{
			name: "ubuntu 24.04 quoted values",
			in:   ubuntu2404,
			want: Info{ID: "ubuntu", VersionID: "24.04", PrettyName: "Ubuntu 24.04.2 LTS"},
		},
		{
			name: "single quoted values",
			in:   "ID='ubuntu'\nVERSION_ID='26.04'\nPRETTY_NAME='Ubuntu 26.04 LTS'\n",
			want: Info{ID: "ubuntu", VersionID: "26.04", PrettyName: "Ubuntu 26.04 LTS"},
		},
		{
			name: "comments and blank lines ignored",
			in:   "# a comment\n\nID=ubuntu\n\n# another\nVERSION_ID=\"24.04\"\n",
			want: Info{ID: "ubuntu", VersionID: "24.04"},
		},
		{
			name: "empty input",
			in:   "",
			want: Info{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Parse(tt.in); got != tt.want {
				t.Errorf("Parse() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestInfoSupported(t *testing.T) {
	tests := []struct {
		name    string
		info    Info
		wantErr string // empty means supported
	}{
		{name: "ubuntu 24.04", info: Info{ID: "ubuntu", VersionID: "24.04"}},
		{name: "ubuntu 26.04", info: Info{ID: "ubuntu", VersionID: "26.04"}},
		{
			name:    "ubuntu 22.04 rejected",
			info:    Info{ID: "ubuntu", VersionID: "22.04", PrettyName: "Ubuntu 22.04.5 LTS"},
			wantErr: "Ubuntu 22.04.5 LTS",
		},
		{
			name:    "debian rejected",
			info:    Info{ID: "debian", VersionID: "12", PrettyName: "Debian GNU/Linux 12 (bookworm)"},
			wantErr: "Debian GNU/Linux 12 (bookworm)",
		},
		{
			name:    "unknown os rejected",
			info:    Info{},
			wantErr: "unknown",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.info.Supported()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Supported() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Supported() = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not mention detected system %q", err, tt.wantErr)
			}
			if !strings.Contains(err.Error(), "24.04") || !strings.Contains(err.Error(), "26.04") {
				t.Errorf("error %q does not list supported versions", err)
			}
		})
	}
}

func TestInfoCodename(t *testing.T) {
	tests := []struct {
		name    string
		info    Info
		want    string
		wantErr bool
	}{
		{name: "Ubuntu 24.04", info: Info{ID: "ubuntu", VersionID: "24.04"}, want: "noble"},
		{name: "Ubuntu 26.04", info: Info{ID: "ubuntu", VersionID: "26.04"}, want: "resolute"},
		{name: "unsupported version", info: Info{ID: "ubuntu", VersionID: "22.04"}, wantErr: true},
		{name: "unsupported distribution", info: Info{ID: "debian", VersionID: "24.04"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.info.Codename()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Codename() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Codename() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("Codename() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseRejectsNonMatchingOS(t *testing.T) {
	if err := Parse(debian12).Supported(); err == nil {
		t.Fatal("debian must not be supported")
	}
}
