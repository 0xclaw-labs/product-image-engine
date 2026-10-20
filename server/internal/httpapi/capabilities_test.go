package httpapi

import (
	"net/http"
	"testing"
)

func TestCapabilitiesTellTheUIFlagsWithoutGrantingPermission(t *testing.T) {
	f := newFixture(t, nil)
	tok := f.stub.mint("cap@example.com", "product-image")
	f.srv.Cfg.SelectiveRevisionEnabled, f.srv.Cfg.BgPlateLockEnabled = false, true
	st, body := f.do(t, "GET", "/api/v1/capabilities", tok, nil)
	if st != http.StatusOK || body["selective_revision"] != false || body["bg_plate_lock"] != true {
		t.Fatalf("capabilities must reflect the process flags: %d %v", st, body)
	}
	f.srv.Cfg.SelectiveRevisionEnabled = true
	st, body = f.do(t, "GET", "/api/v1/capabilities", tok, nil)
	if st != http.StatusOK || body["selective_revision"] != true {
		t.Fatalf("selective revision flag: %d %v", st, body)
	}
	if st, _ := f.do(t, "POST", "/api/v1/capabilities", tok, map[string]any{}); st != http.StatusMethodNotAllowed {
		t.Fatalf("capabilities is read-only: %d", st)
	}
	// It never grants a write path by itself.
	if st, _ := f.do(t, "POST", "/api/v1/projects/unknown/revisions/intent", tok, map[string]any{"text": "背景简单一点"}); st == http.StatusCreated {
		t.Fatal("capabilities must not create revisions")
	}
}
