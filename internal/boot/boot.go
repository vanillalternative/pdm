// Package boot builds the expensive process-wide state the query engine needs
// (embedded-dataset resolvers, the on-disk cache, env-derived fetch options).
// The CLI runs it once per invocation; `pdm serve` runs it once per process —
// which is the whole point of the daemon.
package boot

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bernardosimoes/pdm/data"
	"github.com/bernardosimoes/pdm/internal/admin"
	"github.com/bernardosimoes/pdm/internal/cache"
	"github.com/bernardosimoes/pdm/internal/source"
)

// CacheTTL is the on-disk response cache lifetime.
const CacheTTL = 7 * 24 * time.Hour

// LoadResolvers parses the embedded CAOP datasets. The freguesia resolver is
// optional — a broken dataset must not block queries — so it may be nil even
// on a nil error.
func LoadResolvers() (*admin.Resolver, *admin.FreguesiaResolver, error) {
	resolver, err := admin.NewResolver(data.Municipalities)
	if err != nil {
		return nil, nil, fmt.Errorf("load administrative boundaries: %w", err)
	}
	freguesias, err := admin.NewFreguesiaResolver(data.Freguesias)
	if err != nil {
		freguesias = nil
	}
	return resolver, freguesias, nil
}

// NewCache opens the on-disk response cache.
func NewCache(dir string, disabled bool) (*cache.Cache, error) {
	c, err := cache.New(cache.Options{Dir: dir, TTL: CacheTTL, Disabled: disabled})
	if err != nil {
		return nil, fmt.Errorf("init cache: %w", err)
	}
	return c, nil
}

// FetchOptionsFromEnv reads the fetch-budget overrides into opts:
// PDM_FETCH_TIMEOUT_SECONDS and PDM_FETCH_MAX_ATTEMPTS.
func FetchOptionsFromEnv(opts *source.Options) error {
	if s := strings.TrimSpace(os.Getenv("PDM_FETCH_TIMEOUT_SECONDS")); s != "" {
		seconds, err := strconv.ParseFloat(s, 64)
		if err != nil || seconds <= 0 {
			return fmt.Errorf("invalid PDM_FETCH_TIMEOUT_SECONDS %q", s)
		}
		opts.AttemptTimeout = time.Duration(seconds * float64(time.Second))
	}
	if s := strings.TrimSpace(os.Getenv("PDM_FETCH_MAX_ATTEMPTS")); s != "" {
		attempts, err := strconv.Atoi(s)
		if err != nil || attempts <= 0 {
			return fmt.Errorf("invalid PDM_FETCH_MAX_ATTEMPTS %q", s)
		}
		opts.MaxAttempts = attempts
	}
	return nil
}

// ValidateTruthAPI normalizes a truth-mirror base URL. An empty value is a
// valid explicit disable and returns "".
func ValidateTruthAPI(raw string) (string, error) {
	truthAPI := strings.TrimSpace(raw)
	if truthAPI == "" {
		return "", nil
	}
	u, err := url.Parse(truthAPI)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("invalid truth-mirror URL %q (--truth-api flag or PDM_TRUTH_API env var): expected http(s)://host[/path] with no query or fragment", truthAPI)
	}
	return strings.TrimRight(truthAPI, "/"), nil
}
