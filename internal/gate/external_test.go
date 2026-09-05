package gate

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExternalGatePassFail(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "gate.sh")
	// Reads stdin, writes ok based on repo name.
	body := "#!/bin/sh\n" +
		"repo=$(cat | sed -n 's/.*\"repo\":\"\\([^\"]*\\)\".*/\\1/p')\n" +
		"if [ \"$repo\" = \"good\" ]; then echo '{\"ok\":true}'; else echo '{\"ok\":false,\"evidence\":{\"why\":\"bad\"}}'; fi\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	g := &ExternalGate{GateName: "ex", Argv: []string{script}, Trig: Continuous, Timeout: 5 * time.Second}
	pass, err := g.Check(context.Background(), "good")
	if err != nil || pass.Status != StatusPass {
		t.Fatalf("pass: %+v %v", pass, err)
	}
	fail, err := g.Check(context.Background(), "bad")
	if err != nil || fail.Status != StatusFail {
		t.Fatalf("fail: %+v %v", fail, err)
	}
}
