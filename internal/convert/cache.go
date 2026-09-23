package convert

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Tumetsu/touring-diary-cli/internal/media"
)

// CacheFile is the cache's name inside the output dir.
const CacheFile = ".cache.json"

const cacheVersion = 1

// cache maps a source path (relative to the media dir) to what was produced
// from it and under which parameters (spec 4.5).
type cache struct {
	Version int                   `json:"version"`
	Entries map[string]cacheEntry `json:"entries"`
}

type cacheEntry struct {
	Size    int64  `json:"size"`
	ModTime int64  `json:"mtime"` // Unix nanoseconds
	Params  string `json:"params"`
	// Outputs are paths relative to the out dir; the first is the main file.
	Outputs []string `json:"outputs"`
	Width   int      `json:"width,omitempty"`
	Height  int      `json:"height,omitempty"`
}

func newCache() *cache {
	return &cache{Version: cacheVersion, Entries: map[string]cacheEntry{}}
}

// loadCache reads <outDir>/.cache.json; a missing or unreadable cache is
// treated as empty.
func loadCache(outDir string) *cache {
	c := newCache()
	b, err := os.ReadFile(filepath.Join(outDir, CacheFile))
	if err != nil {
		return c
	}
	var disk cache
	if json.Unmarshal(b, &disk) != nil || disk.Version != cacheVersion || disk.Entries == nil {
		return c
	}
	return &disk
}

// lookup returns the entry for key if it matches the file and params and
// all its outputs still exist.
func (c *cache) lookup(key string, f media.File, params, outDir string) (cacheEntry, bool) {
	e, ok := c.Entries[key]
	if !ok || e.Size != f.Size || e.ModTime != f.ModTime.UnixNano() || e.Params != params || len(e.Outputs) == 0 {
		return e, false
	}
	for _, o := range e.Outputs {
		if _, err := os.Stat(filepath.Join(outDir, filepath.FromSlash(o))); err != nil {
			return e, false
		}
	}
	return e, true
}

// prune drops entries whose outputs are not all in keep, except those
// whose output id retain accepts.
func (c *cache) prune(keep map[string]bool, retain func(id string) bool) {
	for k, e := range c.Entries {
		if retain(ID(k)) {
			continue
		}
		for _, o := range e.Outputs {
			if !keep[o] {
				delete(c.Entries, k)
				break
			}
		}
	}
}

func (c *cache) save(outDir string) error {
	b, err := json.MarshalIndent(c, "", " ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(outDir, CacheFile), append(b, '\n')); err != nil {
		return fmt.Errorf("write cache: %w", err)
	}
	return nil
}
