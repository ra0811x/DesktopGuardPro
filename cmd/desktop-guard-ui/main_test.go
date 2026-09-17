package main

import (
	"path/filepath"
	"testing"
)

func TestApplicationIconPathUsesWorkspaceAssetWhenExecutableDirectoryHasNoIcon(t *testing.T) {
	t.Parallel()

	workingDirectory := filepath.Join("C:", "workspace", "Desktop Guard Pro")
	executable := filepath.Join("C:", "temporary-build", "desktop-guard-ui.exe")
	want := filepath.Join(workingDirectory, "assets", applicationIconFileName)

	got := applicationIconPath(executable, workingDirectory, func(path string) bool {
		return path == want
	})

	if got != want {
		t.Fatalf("applicationIconPath() = %q, want %q", got, want)
	}
}

func TestApplicationIconPathPrefersIconNextToExecutable(t *testing.T) {
	t.Parallel()

	executable := filepath.Join("C:", "release", "desktop-guard-ui.exe")
	want := filepath.Join("C:", "release", applicationIconFileName)

	got := applicationIconPath(executable, filepath.Join("C:", "workspace"), func(path string) bool {
		return path == want
	})

	if got != want {
		t.Fatalf("applicationIconPath() = %q, want %q", got, want)
	}
}
