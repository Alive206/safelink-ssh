//go:build windows

package tunnel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveRestartTargetFromDevExecutableUsesBuiltBinary(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	devBin := filepath.Join(dir, "build", "bin")
	if err := os.MkdirAll(devBin, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(devBin, "safelink-dev.exe")
	prod := filepath.Join(devBin, "SafeLink.exe")
	if err := os.WriteFile(exe, []byte("dev"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prod, []byte("prod"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, args, err := resolveRestartTargetFromExecutable(exe)
	if err != nil {
		t.Fatal(err)
	}
	if got != prod {
		t.Fatalf("program = %q, want %q", got, prod)
	}
	if args != "" {
		t.Fatalf("args = %q, want empty", args)
	}
}

func TestResolveRestartTargetFromDevExecutableRequiresBuiltBinary(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	exe := filepath.Join(dir, "build", "bin", "safelink-dev.exe")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("dev"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := resolveRestartTargetFromExecutable(exe)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "scripts\\build-client.bat") {
		t.Fatalf("error = %q, want build instruction", err)
	}
}
