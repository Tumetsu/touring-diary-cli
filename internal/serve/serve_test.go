package serve

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHandlerNoCache(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "trip.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	Handler(dir).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/trip.json", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "{}" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q", got)
	}
}
