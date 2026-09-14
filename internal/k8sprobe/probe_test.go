package k8sprobe

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFake(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kubectl")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestJobSucceeded(t *testing.T) {
	bin := writeFake(t, `
case "$*" in
  *jsonpath*) echo 1 ;;
  *logs*) echo "probe ok" ;;
  *) exit 1 ;;
esac`)
	ok, logs, err := (&Client{Binary: bin}).JobSucceeded(context.Background(), "dev", "smoke")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("want succeeded")
	}
	if !strings.Contains(logs, "probe ok") {
		t.Fatalf("logs = %q", logs)
	}
}

func TestJobSucceededZero(t *testing.T) {
	bin := writeFake(t, `
case "$*" in
  *jsonpath*) echo 0 ;;
  *logs*) echo "fail" ;;
  *) exit 1 ;;
esac`)
	ok, _, err := (&Client{Binary: bin}).JobSucceeded(context.Background(), "dev", "smoke")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("want not succeeded")
	}
}

func TestJobSucceededGetError(t *testing.T) {
	bin := writeFake(t, `
case "$*" in
  *jsonpath*) exit 1 ;;
  *) exit 0 ;;
esac`)
	_, _, err := (&Client{Binary: bin}).JobSucceeded(context.Background(), "dev", "smoke")
	if err == nil || !strings.Contains(err.Error(), "get job") {
		t.Fatalf("err = %v", err)
	}
}

// kubectl names the cause on stderr; the error has to carry it or every
// failure reads as "exit status 1".
func TestJobSucceededGetErrorCarriesStderr(t *testing.T) {
	bin := writeFake(t, `
case "$*" in
  *jsonpath*) echo 'Error from server (NotFound): jobs.batch "smoke" not found' >&2; exit 1 ;;
  *) exit 0 ;;
esac`)
	_, _, err := (&Client{Binary: bin}).JobSucceeded(context.Background(), "dev", "smoke")
	if err == nil || !strings.Contains(err.Error(), `jobs.batch "smoke" not found`) {
		t.Fatalf("err = %v, want kubectl's stderr in it", err)
	}
}

func TestStderrDetail(t *testing.T) {
	if got := stderrDetail(nil); got != "" {
		t.Fatalf("empty stderr rendered %q", got)
	}
	if got := stderrDetail([]byte("  a\n  b  \n")); got != ": a b" {
		t.Fatalf("got %q", got)
	}
	long := strings.Repeat("x", maxStderrBytes+50)
	if got := stderrDetail([]byte(long)); len(got) > maxStderrBytes+10 || !strings.HasSuffix(got, "…") {
		t.Fatalf("not truncated: %d bytes", len(got))
	}
}

func TestNew(t *testing.T) {
	if New().Binary != "kubectl" {
		t.Fatal(New().Binary)
	}
}
