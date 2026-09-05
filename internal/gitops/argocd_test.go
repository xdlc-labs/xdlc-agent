package gitops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFake(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "argocd")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAppHealthy(t *testing.T) {
	bin := writeFake(t, `echo '{"status":{"sync":{"status":"Synced"},"health":{"status":"Healthy"}}}'`)
	ok, err := (&ArgoCDClient{Binary: bin}).AppHealthy(context.Background(), "myapp")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("want healthy")
	}
}

func TestAppHealthyNotSynced(t *testing.T) {
	bin := writeFake(t, `echo '{"status":{"sync":{"status":"OutOfSync"},"health":{"status":"Healthy"}}}'`)
	ok, err := (&ArgoCDClient{Binary: bin}).AppHealthy(context.Background(), "myapp")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("want not healthy")
	}
}

func TestAppHealthyParseError(t *testing.T) {
	bin := writeFake(t, `echo 'not-json'`)
	_, err := (&ArgoCDClient{Binary: bin}).AppHealthy(context.Background(), "myapp")
	if err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("err = %v", err)
	}
}

func TestAppHealthyExecError(t *testing.T) {
	bin := writeFake(t, `exit 1`)
	_, err := (&ArgoCDClient{Binary: bin}).AppHealthy(context.Background(), "myapp")
	if err == nil || !strings.Contains(err.Error(), "argocd app get") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewArgoCDClient(t *testing.T) {
	if NewArgoCDClient().Binary != "argocd" {
		t.Fatal(NewArgoCDClient().Binary)
	}
}
