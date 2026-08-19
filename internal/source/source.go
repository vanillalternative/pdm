// Package source loads planning-layer features from the various shapes of
// public data: bundled snapshots embedded in the binary, local GeoJSON files,
// WFS GetFeature (GeoJSON output), and OGC API Features collections. Live
// fetches are cached and can fall back to a bundled snapshot when a service is
// unreachable, so the tool stays useful offline and never hammers a service.
package source

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bernardosimoes/pdm/internal/cache"
	"github.com/bernardosimoes/pdm/internal/model"
	"github.com/bernardosimoes/pdm/internal/spatial"
	"github.com/peterstace/simplefeatures/geom"
)

// Loaded is a resolved set of features plus the attribution describing where
// they actually came from (which may differ from the requested source if a
// fallback kicked in).
type Loaded struct {
	Features []spatial.Feature
	Source   model.Source
}

// Loader lazily produces a layer's features.
type Loader func(ctx context.Context) (Loaded, error)

// BBox is a WGS84 lon/lat bounding box used to spatially filter live requests.
type BBox struct {
	MinLon, MinLat, MaxLon, MaxLat float64
}

func (b BBox) empty() bool { return b == BBox{} }

// Options carry the shared runtime configuration for building loaders.
type Options struct {
	Live           bool          // attempt live fetch before falling back to bundled
	Cache          *cache.Cache  // response cache (may be nil)
	HTTP           *http.Client  // HTTP client (defaults applied if nil)
	BBox           *BBox         // spatial filter for live requests
	AttemptTimeout time.Duration // per-request attempt timeout (0 = package default)
	MaxAttempts    int           // live-fetch attempts (0 = package default)

	// TruthAPI is the base URL of the pdms truth-mirror server (empty = mirror
	// disabled). Consulted for recorded zoning before the official sources.
	TruthAPI string
	// Subject is the exact query geometry (WGS84). The truth mirror answers only
	// when its recorded polygons demonstrably cover the subject; the zero value
	// makes the mirror always miss — conservative by construction.
	Subject geom.Geometry
}

func (o Options) client() *http.Client {
	if o.HTTP != nil {
		return o.HTTP
	}
	// Backstop only: each attempt is time-boxed by perAttemptTimeout (a shorter
	// sub-context in fetch), so this client-level ceiling should never fire first.
	return &http.Client{Timeout: 60 * time.Second}
}

// Live-fetch retry budget. The public geoservices are slow when cold and, when
// overloaded, will complete the TLS handshake but never send an HTTP response —
// a connection that just hangs. So each attempt is independently time-boxed
// (perAttemptTimeout) and abandoned rather than allowed to consume the whole
// request budget: a fresh retry may land on a healthy worker. Retries stop only
// when the parent budget is spent or the failure is non-transient (a 4xx).
const maxFetchAttempts = 4

// perAttemptTimeout is a var (not const) so tests can shrink the attempt window.
var perAttemptTimeout = 45 * time.Second

const userAgent = "pdm/0.1 (+https://github.com/bernardosimoes/pdm) planning-data-query"

// Bundled returns a loader over GeoJSON bytes embedded in the binary.
func Bundled(data []byte, meta model.Source) Loader {
	return func(ctx context.Context) (Loaded, error) {
		feats, err := spatial.LoadFeatureCollection(data)
		if err != nil {
			return Loaded{}, fmt.Errorf("bundled %q: %w", meta.Layer, err)
		}
		return Loaded{Features: feats, Source: meta}, nil
	}
}

// File returns a loader over a local GeoJSON FeatureCollection file.
func File(path string, meta model.Source) Loader {
	return func(ctx context.Context) (Loaded, error) {
		feats, err := spatial.LoadFeatureCollectionFile(path)
		if err != nil {
			return Loaded{}, err
		}
		m := meta
		m.URL = path
		return Loaded{Features: feats, Source: m}, nil
	}
}

// Fallback tries loaders in order, moving on only when one errors (an empty but
// successful result is returned as-is). This lets a live source fall back to a
// bundled snapshot when the network is unavailable.
func Fallback(loaders ...Loader) Loader {
	return func(ctx context.Context) (Loaded, error) {
		var lastErr error
		for _, l := range loaders {
			if l == nil {
				continue
			}
			loaded, err := l(ctx)
			if err != nil {
				lastErr = err
				continue
			}
			return loaded, nil
		}
		if lastErr == nil {
			lastErr = fmt.Errorf("no loaders configured")
		}
		return Loaded{}, lastErr
	}
}

// WFSConfig describes a WFS GetFeature request that returns GeoJSON.
type WFSConfig struct {
	BaseURL      string // service endpoint, e.g. https://host/geoserver/wfs
	TypeName     string // feature type, e.g. "planos:ordenamento_tomar"
	Version      string // default "2.0.0"
	OutputFormat string // default "application/json"
	Meta         model.Source
}

// WFS returns a loader that performs a WFS GetFeature request (GeoJSON output),
// cached per request URL. GeoJSON is always lon/lat, so no axis handling needed.
func WFS(cfg WFSConfig, opts Options) Loader {
	return func(ctx context.Context) (Loaded, error) {
		if !opts.Live {
			return Loaded{}, fmt.Errorf("live disabled")
		}
		version := cfg.Version
		if version == "" {
			version = "2.0.0"
		}
		of := cfg.OutputFormat
		if of == "" {
			of = "application/json"
		}
		q := url.Values{}
		q.Set("service", "WFS")
		q.Set("version", version)
		q.Set("request", "GetFeature")
		q.Set("outputFormat", of)
		q.Set("srsName", "EPSG:4326")
		// WFS 2.0 uses TYPENAMES; 1.x uses TYPENAME. Set both for compatibility.
		q.Set("typeNames", cfg.TypeName)
		q.Set("typeName", cfg.TypeName)
		if opts.BBox != nil && !opts.BBox.empty() {
			b := opts.BBox
			// EPSG:4326 axis order for BBOX in WFS 2.0 is lat,lon.
			q.Set("bbox", fmt.Sprintf("%g,%g,%g,%g,urn:ogc:def:crs:EPSG::4326",
				b.MinLat, b.MinLon, b.MaxLat, b.MaxLon))
		}
		reqURL := cfg.BaseURL + "?" + q.Encode()
		data, fetchedAt, fromCache, err := fetch(ctx, opts, reqURL)
		if err != nil {
			return Loaded{}, err
		}
		feats, err := spatial.LoadFeatureCollection(data)
		if err != nil {
			return Loaded{}, fmt.Errorf("WFS %s: %w", cfg.TypeName, err)
		}
		cachePut(opts, reqURL, data, fetchedAt, fromCache)
		m := cfg.Meta
		m.URL = reqURL
		m.Provenance = provenanceForFetch(fromCache)
		m.RetrievedAt = &fetchedAt
		return Loaded{Features: feats, Source: m}, nil
	}
}

// OGCConfig describes an OGC API Features collection request.
type OGCConfig struct {
	ItemsURL string            // e.g. https://host/collections/xyz/items
	Params   map[string]string // extra query params (e.g. attribute filters)
	UseBBox  bool              // include the spatial bbox filter from opts
	Limit    int               // page size (default 1000)
	MaxPages int               // safety cap on pagination (default 20)
	Meta     model.Source
}

// OGC returns a loader for an OGC API Features collection, following "next"
// links up to MaxPages.
func OGC(cfg OGCConfig, opts Options) Loader {
	return func(ctx context.Context) (Loaded, error) {
		if !opts.Live {
			return Loaded{}, fmt.Errorf("live disabled")
		}
		limit := cfg.Limit
		if limit <= 0 {
			limit = 1000
		}
		maxPages := cfg.MaxPages
		if maxPages <= 0 {
			maxPages = 20
		}
		q := url.Values{}
		q.Set("f", "json")
		q.Set("limit", strconv.Itoa(limit))
		for k, v := range cfg.Params {
			q.Set(k, v)
		}
		if cfg.UseBBox && opts.BBox != nil && !opts.BBox.empty() {
			b := opts.BBox
			q.Set("bbox", fmt.Sprintf("%g,%g,%g,%g", b.MinLon, b.MinLat, b.MaxLon, b.MaxLat))
		}
		next := cfg.ItemsURL + "?" + q.Encode()

		var all []spatial.Feature
		var firstFetch time.Time
		var firstFromCache bool
		seen := map[string]bool{}
		for page := 0; page < maxPages && next != ""; page++ {
			if seen[next] { // guard against a self-referential/cyclic next link
				break
			}
			seen[next] = true
			data, fetchedAt, fromCache, err := fetch(ctx, opts, next)
			if err != nil {
				return Loaded{}, err
			}
			if page == 0 {
				firstFetch, firstFromCache = fetchedAt, fromCache
			}
			feats, err := spatial.LoadFeatureCollection(data)
			if err != nil {
				return Loaded{}, fmt.Errorf("OGC items: %w", err)
			}
			cachePut(opts, next, data, fetchedAt, fromCache)
			all = append(all, feats...)
			next = resolveRef(next, nextLink(data))
		}
		m := cfg.Meta
		m.URL = cfg.ItemsURL
		m.Provenance = provenanceForFetch(firstFromCache)
		m.RetrievedAt = &firstFetch
		return Loaded{Features: all, Source: m}, nil
	}
}

// nextLink extracts the rel="next" href from an OGC API Features response.
func nextLink(data []byte) string {
	var doc struct {
		Links []struct {
			Rel  string `json:"rel"`
			Href string `json:"href"`
			Type string `json:"type"`
		} `json:"links"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return ""
	}
	for _, l := range doc.Links {
		if l.Rel == "next" && (l.Type == "" || strings.Contains(l.Type, "json")) {
			return l.Href
		}
	}
	return ""
}

// resolveRef resolves a possibly-relative href against the base request URL so
// services that return relative rel=next links still paginate.
func resolveRef(base, href string) string {
	if href == "" {
		return ""
	}
	b, err := url.Parse(base)
	if err != nil {
		return href
	}
	r, err := url.Parse(href)
	if err != nil {
		return href
	}
	return b.ResolveReference(r).String()
}

func provenanceForFetch(fromCache bool) model.Provenance {
	// Labelled by where the bytes actually came from, not by elapsed time (a
	// slow multi-page live fetch must not be mislabelled as cache).
	if fromCache {
		return model.ProvenanceOfficialCache
	}
	return model.ProvenanceOfficialLive
}

// fetch performs an HTTP GET (serving from cache when possible), returning the
// body, the effective fetch time (cache time on a hit, now on a miss), and
// whether it came from cache. Live responses are NOT cached here: callers cache
// via cachePut only after the body parses, so a transient garbage response
// (maintenance page, truncated body) can never poison the cache for its TTL.
// Transient failures are retried: the public geoservices are slow when cold and
// intermittently answer 5xx.
func fetch(ctx context.Context, opts Options, reqURL string) ([]byte, time.Time, bool, error) {
	if opts.Cache != nil {
		if e, ok := opts.Cache.Get(reqURL); ok {
			return e.Data, e.FetchedAt, true, nil
		}
	}
	var lastErr error
	attempts := maxFetchAttempts
	if opts.MaxAttempts > 0 {
		attempts = opts.MaxAttempts
	}
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, time.Time{}, false, ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}
		// Time-box the attempt without exceeding the remaining parent budget
		// (WithTimeout keeps the earlier of the two deadlines), so a hung backend
		// is dropped and retried instead of blocking the whole query on one call.
		timeout := perAttemptTimeout
		if opts.AttemptTimeout > 0 {
			timeout = opts.AttemptTimeout
		}
		attemptCtx, cancel := context.WithTimeout(ctx, timeout)
		body, retryable, err := fetchOnce(attemptCtx, opts, reqURL)
		cancel()
		if err == nil {
			return body, timeNow(), false, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			// The whole request budget is spent — surface the clean deadline error.
			return nil, time.Time{}, false, ctx.Err()
		}
		if !retryable {
			break
		}
	}
	return nil, time.Time{}, false, lastErr
}

// fetchOnce performs a single GET. retryable reports whether the failure is
// transient (network error, 5xx, or 429) and worth another attempt.
func fetchOnce(ctx context.Context, opts Options, reqURL string) (body []byte, retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json, application/geo+json")
	resp, err := opts.client().Do(req)
	if err != nil {
		// Network error or an attempt that timed out (including a backend that
		// accepts the connection but never answers). Transient as a class; whether
		// to actually retry is fetch's call, based on the remaining parent budget.
		return nil, true, fmt.Errorf("GET %s: %w", trim(reqURL), err)
	}
	defer resp.Body.Close()
	body, err = io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, true, fmt.Errorf("GET %s: %w", trim(reqURL), err)
	}
	if resp.StatusCode != http.StatusOK {
		transient := resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests
		return nil, transient, fmt.Errorf("GET %s: status %d", trim(reqURL), resp.StatusCode)
	}
	return body, false, nil
}

// cachePut stores a successfully parsed live response. No-op for cache hits.
func cachePut(opts Options, reqURL string, data []byte, fetchedAt time.Time, fromCache bool) {
	if opts.Cache == nil || fromCache {
		return
	}
	_ = opts.Cache.Put(reqURL, reqURL, data, fetchedAt)
}

func trim(s string) string {
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}

// timeNow is indirected so tests could stub it; uses wall clock.
var timeNow = time.Now
