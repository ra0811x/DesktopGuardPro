package buildinfo

import "strings"

// Version is replaced by the release builder through a Go linker flag.
var Version = "development"

func ProductVersion() string {
	version := strings.TrimSpace(Version)
	if version == "" {
		return "development"
	}
	return version
}
