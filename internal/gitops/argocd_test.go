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

// TestAppHealthyExecErrorCarriesStderr is issue #45's first half. The
// CLI's stderr used to be discarded, so a typo'd argocd_app, an expired
// session, a missing kubeconfig and an RBAC denial were all
// indistinguishable "exit status 20".
func TestAppHealthyExecErrorCarriesStderr(t *testing.T) {
	cases := []struct {
		name   string
		script string
		want   string
	}{
		{
			"unknown app",
			`echo 'rpc error: code = NotFound desc = applications.argoproj.io "dev-typo" not found' >&2; exit 20`,
			`"dev-typo" not found`,
		},
		{
			"expired session",
			`echo 'rpc error: code = Unauthenticated desc = token has expired' >&2; exit 20`,
			"token has expired",
		},
		{
			"no kubeconfig",
			`echo 'error: could not read kubeconfig: no such file or directory' >&2; exit 1`,
			"could not read kubeconfig",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bin := writeFake(t, c.script)
			_, err := (&ArgoCDClient{Binary: bin}).AppHealthy(context.Background(), "dev-typo")
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error does not say why the CLI failed:\n%s", err)
			}
			// The old message, still there so the failing command is named.
			if !strings.Contains(err.Error(), "argocd app get dev-typo") {
				t.Fatalf("error no longer names the command:\n%s", err)
			}
		})
	}
}

// TestAppHealthyStderrIsOneBoundedLine: the message lands in gate
// evidence, a BACKLOG.md line and an audit row, all of which are one
// line per record.
func TestAppHealthyStderrIsOneBoundedLine(t *testing.T) {
	bin := writeFake(t, `awk 'BEGIN{for(i=0;i<400;i++) print "noise line", i}' >&2; exit 20`)
	_, err := (&ArgoCDClient{Binary: bin}).AppHealthy(context.Background(), "myapp")
	if err == nil {
		t.Fatal("want an error")
	}
	msg := err.Error()
	if strings.ContainsAny(msg, "\n\r\t") {
		t.Fatalf("error spans multiple lines:\n%q", msg)
	}
	if len(msg) > maxStderrBytes+200 {
		t.Fatalf("error is %d bytes, want it truncated near %d", len(msg), maxStderrBytes)
	}
	if !strings.HasSuffix(msg, "…") {
		t.Fatalf("truncation not marked:\n%s", msg)
	}
}

// TestAppHealthySilentFailureStillReadable: a CLI that prints nothing on
// stderr must not leave a dangling ": ".
func TestAppHealthySilentFailureStillReadable(t *testing.T) {
	bin := writeFake(t, `exit 20`)
	_, err := (&ArgoCDClient{Binary: bin}).AppHealthy(context.Background(), "myapp")
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.HasSuffix(err.Error(), ": ") || strings.HasSuffix(err.Error(), ":") {
		t.Fatalf("empty stderr left a dangling separator:\n%q", err.Error())
	}
}
