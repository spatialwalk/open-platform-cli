package avtkitcli

import (
	"fmt"
	"io"
	"strings"
)

const (
	defaultVersion   = "0.0.0-dev"
	defaultCommit    = "unknown"
	defaultBuildDate = "unknown"
)

var (
	version   = defaultVersion
	commit    = defaultCommit
	buildDate = defaultBuildDate
)

type buildInfo struct {
	Version   string
	Commit    string
	BuildDate string
}

func currentBuildInfo() buildInfo {
	return buildInfo{
		Version:   fallbackVersion(version),
		Commit:    fallbackValue(commit, defaultCommit),
		BuildDate: fallbackValue(buildDate, defaultBuildDate),
	}
}

func fallbackVersion(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return defaultVersion
	}
	return trimmed
}

func fallbackValue(value string, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}

func printVersion(w io.Writer) {
	info := currentBuildInfo()
	fmt.Fprintf(w, "%s %s\n", cliName, info.Version)
	fmt.Fprintf(w, "Git commit: %s\n", info.Commit)
	fmt.Fprintf(w, "Build date: %s\n", info.BuildDate)
}
