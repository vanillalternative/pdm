// The truth-mirror loader. The pdms web store records the zoning polygons
// returned by past official queries in PostGIS and re-serves them over a small
// JSON API. Consulting that mirror first answers repeat point and parcel
// queries without touching the (slow, periodically overloaded) official
// geoservices. The
// mirror is only ever a shortcut, never an authority of its own: any outcome
// that is not a demonstrated hit — unconfigured, no subject, network error,
// bad payload, empty result, or no feature actually covering the subject — is
// returned as an error so Fallback moves on to the official source.
package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bernardosimoes/pdm/internal/model"
	"github.com/bernardosimoes/pdm/internal/spatial"
	"github.com/peterstace/simplefeatures/geom"
)

// TruthConfig describes one truth-mirror zoning request.
type TruthConfig struct {
	BaseURL string // mirror base URL, no trailing slash
	Code    string // municipality dtcc code
	Meta    model.Source
}

// ErrTruthMiss marks the mirror outcomes that are ordinary misses (nothing
// recorded for the subject) rather than failures.
var ErrTruthMiss = errors.New("truth mirror has no recorded coverage")

// truthTimeout bounds the single mirror attempt. The mirror is a local
// shortcut in front of the official sources, so it must fail fast — a var so
// tests can shrink it.
var truthTimeout = 2 * time.Second

// truthMarkerProp flags a feature as served by the mirror, so classification
// downstream reads the recorded properties instead of the source schema.
const truthMarkerProp = "_pdm_mirror"

// FromTruthMirror reports whether a feature was served by the truth mirror.
func FromTruthMirror(f spatial.Feature) bool {
	v, _ := f.Props[truthMarkerProp].(bool)
	return v
}

// truthEnvelope is the RFC-7946 foreign member the mirror attaches to its
// FeatureCollections.
type truthEnvelope struct {
	RecordedFrom []string `json:"recorded_from"`
	UpdatedAt    string   `json:"updated_at"`
	Count        int      `json:"count"`
	NextAfter    any      `json:"next_after"`
}

// Truth returns a loader over the pdms truth mirror for a point or polygon
// query. Polygon answers are accepted only when one complete mirror page
// covers the full subject; otherwise the official-source fallback runs. It
// performs exactly one attempt, never touches the disk cache, and errors on
// every outcome that is not a verified hit (see package comment).
func Truth(cfg TruthConfig, opts Options) Loader {
	return func(ctx context.Context) (Loaded, error) {
		if cfg.BaseURL == "" || cfg.Code == "" {
			return Loaded{}, fmt.Errorf("not configured: %w", ErrTruthMiss)
		}
		pt, isPoint := opts.Subject.AsPoint()
		if opts.Subject.IsEmpty() {
			return Loaded{}, fmt.Errorf("subject is empty: %w", ErrTruthMiss)
		}
		base, err := url.Parse(cfg.BaseURL)
		if err != nil {
			return Loaded{}, fmt.Errorf("truth mirror: base URL %q: %w", cfg.BaseURL, err)
		}
		u := base.JoinPath("api", "truth", "zoning")
		q := url.Values{}
		q.Set("code", cfg.Code)
		if cfg.Meta.Layer != "" {
			q.Set("layer", cfg.Meta.Layer)
		}
		if isPoint {
			xy, ok := pt.XY()
			if !ok {
				return Loaded{}, fmt.Errorf("subject point is empty: %w", ErrTruthMiss)
			}
			q.Set("lat", strconv.FormatFloat(xy.Y, 'f', -1, 64))
			q.Set("lon", strconv.FormatFloat(xy.X, 'f', -1, 64))
		} else {
			min, max, ok := opts.Subject.Envelope().MinMaxXYs()
			if !ok {
				return Loaded{}, fmt.Errorf("subject has no envelope: %w", ErrTruthMiss)
			}
			q.Set("bbox", strings.Join([]string{
				strconv.FormatFloat(min.X, 'f', -1, 64),
				strconv.FormatFloat(min.Y, 'f', -1, 64),
				strconv.FormatFloat(max.X, 'f', -1, 64),
				strconv.FormatFloat(max.Y, 'f', -1, 64),
			}, ","))
			q.Set("limit", "1000")
		}
		// Any query/fragment on the configured base is discarded — the request
		// must target the API path even if a sloppy base slipped past validation.
		u.RawQuery = q.Encode()
		u.Fragment = ""
		reqURL := u.String()

		attemptCtx, cancel := context.WithTimeout(ctx, truthTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, reqURL, nil)
		if err != nil {
			return Loaded{}, fmt.Errorf("truth mirror: %w", err)
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept", "application/geo+json, application/json")
		resp, err := opts.client().Do(req)
		if err != nil {
			return Loaded{}, fmt.Errorf("truth mirror: GET %s: %w", trim(reqURL), err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20+1))
		if err != nil {
			return Loaded{}, fmt.Errorf("truth mirror: GET %s: %w", trim(reqURL), err)
		}
		if resp.StatusCode != http.StatusOK {
			return Loaded{}, fmt.Errorf("truth mirror: GET %s: status %d", trim(reqURL), resp.StatusCode)
		}
		if len(body) > 64<<20 {
			return Loaded{}, fmt.Errorf("truth mirror: GET %s: body exceeds 64 MiB", trim(reqURL))
		}
		feats, err := spatial.LoadFeatureCollection(body)
		if err != nil {
			return Loaded{}, fmt.Errorf("truth mirror: %w", err)
		}
		if len(feats) == 0 {
			return Loaded{}, fmt.Errorf("no features recorded here: %w", ErrTruthMiss)
		}
		var doc struct {
			PDMS *truthEnvelope `json:"pdms"`
		}
		_ = json.Unmarshal(body, &doc)
		// Defense in depth: point mode checks containment again. Polygon mode
		// additionally requires an unpaginated response and proves that the
		// union of recorded zoning polygons covers every part of the parcel.
		if isPoint {
			covered := false
			for _, f := range feats {
				if spatial.Intersects(opts.Subject, f.Geometry) {
					covered = true
					break
				}
			}
			if !covered {
				return Loaded{}, fmt.Errorf("recorded polygons do not cover the point: %w", ErrTruthMiss)
			}
		} else {
			if doc.PDMS == nil || doc.PDMS.Count != len(feats) || doc.PDMS.NextAfter != nil {
				return Loaded{}, fmt.Errorf("recorded polygon page is incomplete: %w", ErrTruthMiss)
			}
			geoms := make([]geom.Geometry, 0, len(feats))
			for _, f := range feats {
				geoms = append(geoms, f.Geometry)
			}
			union, err := spatial.UnionAll(geoms)
			if err != nil || !spatial.Covers(union, opts.Subject) {
				return Loaded{}, fmt.Errorf("recorded polygons do not fully cover the parcel: %w", ErrTruthMiss)
			}
		}
		for i := range feats {
			if feats[i].Props == nil {
				feats[i].Props = map[string]any{}
			}
			feats[i].Props[truthMarkerProp] = true
		}
		m := cfg.Meta
		m.URL = reqURL
		m.Provenance = model.ProvenanceRecordedMirror
		now := timeNow()
		m.RetrievedAt = &now
		if doc.PDMS != nil {
			if len(doc.PDMS.RecordedFrom) > 0 {
				m.Name += " (orig.: " + strings.Join(doc.PDMS.RecordedFrom, ", ") + ")"
			}
			if t, err := time.Parse(time.RFC3339, doc.PDMS.UpdatedAt); err == nil {
				m.RetrievedAt = &t
			}
		}
		return Loaded{Features: feats, Source: m}, nil
	}
}
