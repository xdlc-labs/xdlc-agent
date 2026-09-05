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

func TestNew(t *testing.T) {
	if New().Binary != "kubectl" {
		t.Fatal(New().Binary)
	}
}
