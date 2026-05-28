package version

import (
	"fmt"
	"runtime/debug"
	"strings"
)

var (
	version = ""
	commit  = ""
	branch  = ""
	dirty   = ""
)

func String() string {
	if version != "" && commit == "" && branch == "" {
		if dirty == "true" && !strings.HasSuffix(version, "-dirty") {
			return version + "-dirty"
		}
		return version
	}
	base := version
	if base == "" {
		base = "v0.0.1"
	}
	br := sanitizePart(branch)
	cm := commit
	info, ok := debug.ReadBuildInfo()
	modified := dirty == "true"
	if ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				if cm == "" {
					cm = setting.Value
				}
			case "vcs.modified":
				modified = modified || setting.Value == "true"
			}
		}
	}
	if br == "" {
		br = "local"
	}
	if cm == "" {
		cm = "unknown"
	}
	if len(cm) > 12 {
		cm = cm[:12]
	}
	s := fmt.Sprintf("%s-%s-%s", base, br, cm)
	if modified && !strings.HasSuffix(s, "-dirty") {
		s += "-dirty"
	}
	return s
}

func sanitizePart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
