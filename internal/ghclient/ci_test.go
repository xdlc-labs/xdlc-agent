package ghclient

import "testing"

func TestParseRunURL(t *testing.T) {
	owner, repo, id, err := ParseRunURL("https://github.com/acme/svc/actions/runs/12345")
	if err != nil {
		t.Fatal(err)
	}
	if owner != "acme" || repo != "svc" || id != 12345 {
		t.Fatalf("got %s/%s/%d", owner, repo, id)
	}
	if _, _, _, err := ParseRunURL("https://example.com/nope"); err == nil {
		t.Fatal("expected error")
	}
}
