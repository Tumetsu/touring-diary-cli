package build

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/tuomassalmi/touring-diary/web"
)

func TestEmbeddedAssets(t *testing.T) {
	for _, name := range []string{"index.html", "app.js", "style.css", "vendor/leaflet/leaflet.js", "vendor/markercluster/leaflet.markercluster.js"} {
		if _, err := fs.Stat(web.FS, name); err != nil {
			t.Errorf("embedded FS lacks %s: %v", name, err)
		}
	}
}

func TestCopyWebAssets(t *testing.T) {
	out := t.TempDir()
	if err := copyWebAssets(out); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"index.html", "app.js", "style.css", "vendor/leaflet/images/layers.png"} {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(name))); err != nil {
			t.Errorf("%s not written: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "vendor", "VERSIONS.md")); err == nil {
		t.Error("VERSIONS.md should not be copied")
	}
}
