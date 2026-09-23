package build

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tuomassalmi/touring-diary/web"
)

// copyWebAssets writes the embedded frontend (index.html, app.js, style.css,
// vendor/) into outDir, overwriting existing files.
func copyWebAssets(outDir string) error {
	return copyFS(web.FS, outDir)
}

func copyFS(src fs.FS, outDir string) error {
	return fs.WalkDir(src, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." || filepath.Ext(path) == ".go" || d.Name() == "VERSIONS.md" {
			return nil
		}
		dst := filepath.Join(outDir, filepath.FromSlash(path))
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := fs.ReadFile(src, path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return fmt.Errorf("copy web asset %s: %w", path, err)
		}
		return nil
	})
}
