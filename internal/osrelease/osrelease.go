// Package osrelease parses /etc/os-release and checks whether the
// detected distribution is supported by ft.
package osrelease

import (
	"fmt"
	"slices"
	"strings"
)

// Path is the standard location of the os-release file on Linux.
const Path = "/etc/os-release"

// supported maps distribution ID to the version IDs ft supports.
var supported = map[string][]string{
	"ubuntu": {"24.04", "26.04"},
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
	if slices.Contains(supported[i.ID], i.VersionID) {
		return nil
	}
	detected := i.PrettyName
	if detected == "" {
		detected = "unknown system"
	}
	return fmt.Errorf("unsupported system: %s (supported: Ubuntu 24.04, Ubuntu 26.04)", detected)
}
