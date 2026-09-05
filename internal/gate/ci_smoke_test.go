package gate

import (
	"context"
	"errors"
	"testing"
)

func TestCIGate(t *testing.T) {
	g := &CIGate{
		GetStatus: func(_ context.Context, repo string) (string, string, error) {
			if repo != "acme/svc" {
				t.Fatalf("repo = %q", repo)
			}
			return "success", "https://example/run/1", nil
		},
	}
	if g.Name() != "ci" || g.Trigger() != OnPush {
		t.Fatalf("meta name=%s trigger=%s", g.Name(), g.Trigger())
	}
	res, err := g.Check(context.Background(), "acme/svc")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusPass || res.Evidence["conclusion"] != "success" {
		t.Fatalf("%+v", res)
	}

	g.GetStatus = func(context.Context, string) (string, string, error) {
		return "failure", "https://example/run/2", nil
	}
	res, err = g.Check(context.Background(), "acme/svc")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusFail {
		t.Fatalf("%+v", res)
	}

	g.GetStatus = func(context.Context, string) (string, string, error) {
		return "", "", errors.New("boom")
	}
	if _, err := g.Check(context.Background(), "acme/svc"); err == nil {
		t.Fatal("expected error")
	}
}

func TestSmokeGate(t *testing.T) {
	g := &SmokeGate{
		ArgoCDApp: "app",
		ProbeJob:  "job",
		AppHealthy: func(_ context.Context, app string) (bool, error) {
			if app != "app" {
				t.Fatalf("app = %q", app)
			}
			return true, nil
		},
		ProbeResult: func(_ context.Context, job string) (bool, string, error) {
			if job != "job" {
				t.Fatalf("job = %q", job)
			}
			return true, "ok", nil
		},
	}
	if g.Name() != "dev-smoke" || g.Trigger() != OnSync {
		t.Fatalf("meta name=%s trigger=%s", g.Name(), g.Trigger())
	}
	res, err := g.Check(context.Background(), "unused")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusPass || res.Evidence["logs"] != "ok" {
		t.Fatalf("%+v", res)
	}

	g.AppHealthy = func(context.Context, string) (bool, error) { return false, nil }
	res, err = g.Check(context.Background(), "unused")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusFail || res.Evidence["reason"] == nil {
		t.Fatalf("%+v", res)
	}

	g.AppHealthy = func(context.Context, string) (bool, error) { return true, nil }
	g.ProbeResult = func(context.Context, string) (bool, string, error) { return false, "boom", nil }
	res, err = g.Check(context.Background(), "unused")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusFail {
		t.Fatalf("%+v", res)
	}
}
