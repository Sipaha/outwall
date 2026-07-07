package daemon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A missing hashed asset must 404 rather than fall back to index.html — otherwise the browser
// receives text/html for a .js/.css request and fails with a confusing MIME-type error, masking
// an index.html/assets hash mismatch.
func TestStaticUIMissingAssetIs404(t *testing.T) {
	h := staticUI()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/index-deadbeef.css", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing asset: status = %d, want 404; body=%q", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "<html") {
		t.Fatalf("missing asset served HTML fallback: %q", rec.Body.String())
	}
}

// Unknown non-asset paths are client-side routes and must fall back to index.html so the SPA
// router can handle them.
func TestStaticUIClientRouteFallsBackToIndex(t *testing.T) {
	h := staticUI()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/access", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("client route: status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<div id=\"root\">") {
		t.Fatalf("client route did not serve index.html: %q", rec.Body.String())
	}
}
