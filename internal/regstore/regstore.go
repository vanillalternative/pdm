// Package regstore is the data-driven registry of parsed PDM written
// regulations, keyed by the 4-digit CAOP municipality code (dtcc). It loads the
// bundled data/regulamentos/<dtcc>.json files once and hands the query engine
// the regulation for a resolved municipality, so nationwide coverage grows by
// adding data files — never per-municipality Go code.
package regstore

import (
	"io/fs"
	"path/filepath"
	"strings"
	"sync"

	"github.com/bernardosimoes/pdm/data"
	"github.com/bernardosimoes/pdm/internal/reg"
)

var (
	once   sync.Once
	stores map[string]*reg.Store
)

// load parses every data/regulamentos/<dtcc>.json into a store keyed by dtcc.
// index.json and visits.json are manifests, not regulations, so they are
// skipped. A file that fails to parse is skipped (never fatal) — a missing
// regulation just falls back to the zoning-only path.
func load() {
	stores = map[string]*reg.Store{}
	entries, err := fs.ReadDir(data.Regulamentos, "regulamentos")
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		dtcc := strings.TrimSuffix(name, ".json")
		if dtcc == "index" || dtcc == "visits" {
			continue
		}
		b, err := fs.ReadFile(data.Regulamentos, filepath.ToSlash(filepath.Join("regulamentos", name)))
		if err != nil {
			continue
		}
		if s, err := reg.Load(b); err == nil {
			stores[dtcc] = s
		}
	}
}

// ForCode returns the parsed regulation for a municipality's dtcc code, or nil
// if none is bundled.
func ForCode(dtcc string) *reg.Store {
	once.Do(load)
	if dtcc == "" {
		return nil
	}
	return stores[dtcc]
}
