// Package osrelease parses /etc/os-release and checks whether the
// detected distribution is supported by ft.
package osrelease

import (
	"fmt"
	"strings"
)

// Path is the standard location of the os-release file on Linux.
const Path = "/etc/os-release"

// supported maps distribution and version IDs to their apt repository
// codenames, keeping release support metadata in one place.
var supported = map[string]map[string]string{
	"ubuntu": {
		"24.04": "noble",
		"26.04": "resolute",
	},
}

// Info holds the fields ft cares about from an os-release file.
type Info struct {
	ID         string // e.g. "ubuntu"
	VersionID  string // e.g. "24.04"
	PrettyName string // e.g. "Ubuntu 24.04.2 LTS"
}

// Parse extracts Info from the contents of an os-release file.
func Parse(data string) Info {
	var info Info
	for line := range strings.SplitSeq(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.Trim(value, `"'`)
		switch key {
		case "ID":
			info.ID = value
		case "VERSION_ID":
			info.VersionID = value
		case "PRETTY_NAME":
			info.PrettyName = value
		}
	}
	return info
}

// Supported reports whether the detected OS is in ft's support matrix.
// It returns nil when supported, otherwise an error naming the detected
// system and the supported ones.
func (i Info) Supported() error {
	_, err := i.Codename()
	return err
}

// Codename returns the apt repository codename for a supported release.
func (i Info) Codename() (string, error) {
	if codename, ok := supported[i.ID][i.VersionID]; ok {
		return codename, nil
	}
	detected := i.PrettyName
	if detected == "" {
		detected = "unknown system"
	}
	return "", fmt.Errorf("unsupported system: %s (supported: Ubuntu 24.04, Ubuntu 26.04)", detected)
}
