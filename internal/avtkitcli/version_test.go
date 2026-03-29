package avtkitcli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunVersionFlagUsesDefaultBuildInfo(t *testing.T) {
	originalVersion := version
	originalCommit := commit
	originalBuildDate := buildDate
	version = ""
	commit = ""
	buildDate = ""
	t.Cleanup(func() {
		version = originalVersion
		commit = originalCommit
		buildDate = originalBuildDate
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := Run(context.Background(), []string{"--version"}, Streams{
		Stdout: &stdout,
		Stderr: &stderr,
	}); err != nil {
		t.Fatalf("Run(--version): %v", err)
	}

	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}

	output := stdout.String()
	for _, want := range []string{
		"avtkit 0.0.0-dev",
		"Git commit: unknown",
		"Build date: unknown",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected version output to contain %q, got %q", want, output)
		}
	}
}

func TestRunVersionCommandUsesInjectedBuildInfo(t *testing.T) {
	originalVersion := version
	originalCommit := commit
	originalBuildDate := buildDate
	version = "1.2.3"
	commit = "abcdef123456"
	buildDate = "2026-03-29T08:15:00Z"
	t.Cleanup(func() {
		version = originalVersion
		commit = originalCommit
		buildDate = originalBuildDate
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := Run(context.Background(), []string{"version"}, Streams{
		Stdout: &stdout,
		Stderr: &stderr,
	}); err != nil {
		t.Fatalf("Run(version): %v", err)
	}

	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}

	output := stdout.String()
	for _, want := range []string{
		"avtkit 1.2.3",
		"Git commit: abcdef123456",
		"Build date: 2026-03-29T08:15:00Z",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected version output to contain %q, got %q", want, output)
		}
	}
}
