// Package cache provides a small on-disk cache for fetched public data, so the
// tool does not hammer public geoservices. Entries are keyed by a caller
// supplied key (typically a request URL) and stored with the fetch timestamp.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Cache is a filesystem-backed byte cache with per-entry timestamps and TTL.
type Cache struct {
	dir     string
	ttl     time.Duration
	disable bool
}

// Entry is cached payload plus the time it was fetched.
type Entry struct {
	Data      []byte
	FetchedAt time.Time
}

type meta struct {
	Key       string    `json:"key"`
	FetchedAt time.Time `json:"fetched_at"`
	URL       string    `json:"url,omitempty"`
}

// Options configure a Cache.
type Options struct {
	Dir      string        // cache root; defaults to os.UserCacheDir()/pdm
	TTL      time.Duration // entries older than this are treated as missing (0 = never expire)
	Disabled bool          // if true, all reads miss and writes are no-ops
}

// New creates a Cache, creating the directory if needed.
func New(opts Options) (*Cache, error) {
	dir := opts.Dir
	if dir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			base = os.TempDir()
		}
		dir = filepath.Join(base, "pdm")
	}
	if !opts.Disabled {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return &Cache{dir: dir, ttl: opts.TTL, disable: opts.Disabled}, nil
}

func (c *Cache) path(key, ext string) string {
	sum := sha256.Sum256([]byte(key))
	name := hex.EncodeToString(sum[:16])
	return filepath.Join(c.dir, name+ext)
}

// Get returns the cached entry for key, or ok=false on miss/expiry/disabled.
func (c *Cache) Get(key string) (Entry, bool) {
	if c == nil || c.disable {
		return Entry{}, false
	}
	metaBytes, err := os.ReadFile(c.path(key, ".meta.json"))
	if err != nil {
		return Entry{}, false
	}
	var m meta
	if json.Unmarshal(metaBytes, &m) != nil {
		return Entry{}, false
	}
	if c.ttl > 0 && time.Since(m.FetchedAt) > c.ttl {
		return Entry{}, false
	}
	data, err := os.ReadFile(c.path(key, ".data"))
	if err != nil {
		return Entry{}, false
	}
	return Entry{Data: data, FetchedAt: m.FetchedAt}, true
}

// Put stores data for key with the given fetch time and optional source URL.
func (c *Cache) Put(key, url string, data []byte, fetchedAt time.Time) error {
	if c == nil || c.disable {
		return nil
	}
	if err := os.WriteFile(c.path(key, ".data"), data, 0o644); err != nil {
		return err
	}
	m := meta{Key: key, FetchedAt: fetchedAt, URL: url}
	b, _ := json.MarshalIndent(m, "", "  ")
	return os.WriteFile(c.path(key, ".meta.json"), b, 0o644)
}

// Dir returns the cache directory.
func (c *Cache) Dir() string { return c.dir }
