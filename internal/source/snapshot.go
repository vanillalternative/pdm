// The snapshot-store probe. The pdms web store holds complete national
// constraint datasets (APA albufeiras and coastal layers) harvested in bulk
// from the official ArcGIS services, which are only erratically reachable.
// Unlike the truth mirror — partial by construction, so a miss means
// "unknown" — a snapshot covers the entire dataset: an empty answer from a
// loaded snapshot is a real "no". Any failure (unconfigured, dataset not
// loaded, network error, bad payload) is returned as an error so the caller
// falls back to the live official probe.
package source

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/bernardosimoes/pdm/internal/model"
	"github.com/bernardosimoes/pdm/internal/spatial"
)

// SnapshotConfig describes one snapshot-store constraint probe.
type SnapshotConfig struct {
	BaseURL   string // snapshot store base URL, no trailing slash
	Dataset   string // dataset name as loaded by the harvester (e.g. "albufeiras")
	DistanceM float64
	Meta      model.Source
}

// snapshotTimeout bounds the single snapshot attempt: it is a local DB lookup
// behind a loopback HTTP hop, so it must fail fast for the live fallback to
// still fit the layer budget. A var so tests can shrink it.
var snapshotTimeout = 2 * time.Second

// SnapshotProbe builds a presence probe over the pdms snapshot store with the
// same semantics as ArcGISProbe: the subject envelope intersects (or is within
// DistanceM of) the dataset's polygons, evaluated server-side, and only
// attributes come back. Each returned feature carries a "_distance_m" prop
// (0 = intersects) so callers can layer belt logic without extra round trips.
func SnapshotProbe(cfg SnapshotConfig, opts Options) func(ctx context.Context, subject BBox) ([]spatial.Feature, model.Source, error) {
	return func(ctx context.Context, subject BBox) ([]spatial.Feature, model.Source, error) {
		if cfg.BaseURL == "" {
			return nil, model.Source{}, fmt.Errorf("snapshot store not configured")
		}
		base, err := url.Parse(cfg.BaseURL)
		if err != nil {
			return nil, model.Source{}, fmt.Errorf("snapshot store: base URL %q: %w", cfg.BaseURL, err)
		}
		u := base.JoinPath("api", "truth", "constraints")
		q := url.Values{}
		q.Set("dataset", cfg.Dataset)
		q.Set("bbox", fmt.Sprintf("%g,%g,%g,%g", subject.MinLon, subject.MinLat, subject.MaxLon, subject.MaxLat))
		if cfg.DistanceM > 0 {
			q.Set("distance_m", strconv.FormatFloat(cfg.DistanceM, 'f', -1, 64))
		}
		u.RawQuery = q.Encode()
		u.Fragment = ""
		reqURL := u.String()

		attemptCtx, cancel := context.WithTimeout(ctx, snapshotTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, model.Source{}, fmt.Errorf("snapshot store: %w", err)
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept", "application/json")
		resp, err := opts.client().Do(req)
		if err != nil {
			return nil, model.Source{}, fmt.Errorf("snapshot store: GET %s: %w", trim(reqURL), err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		if err != nil {
			return nil, model.Source{}, fmt.Errorf("snapshot store: GET %s: %w", trim(reqURL), err)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, model.Source{}, fmt.Errorf("snapshot store: GET %s: status %d", trim(reqURL), resp.StatusCode)
		}
		var doc struct {
			PDMS *struct {
				Dataset     string `json:"dataset"`
				HarvestedAt string `json:"harvested_at"`
			} `json:"pdms"`
			Features []struct {
				Properties map[string]any `json:"properties"`
				DistanceM  float64        `json:"distance_m"`
			} `json:"features"`
		}
		if err := json.Unmarshal(body, &doc); err != nil {
			return nil, model.Source{}, fmt.Errorf("snapshot store: GET %s: %w", trim(reqURL), err)
		}
		if doc.PDMS == nil || doc.PDMS.Dataset != cfg.Dataset {
			return nil, model.Source{}, fmt.Errorf("snapshot store: GET %s: malformed envelope", trim(reqURL))
		}

		feats := make([]spatial.Feature, 0, len(doc.Features))
		for _, f := range doc.Features {
			props := f.Properties
			if props == nil {
				props = map[string]any{}
			}
			props["_distance_m"] = f.DistanceM
			feats = append(feats, spatial.Feature{Props: props})
		}
		src := cfg.Meta
		src.URL = reqURL
		src.Provenance = model.ProvenanceOfficialSnapshot
		// The data's age is the harvest date, not the request time — staleness
		// must not hide behind a fresh-looking retrieval stamp.
		if t, err := time.Parse(time.RFC3339, doc.PDMS.HarvestedAt); err == nil {
			src.RetrievedAt = &t
		} else {
			now := timeNow()
			src.RetrievedAt = &now
		}
		return feats, src, nil
	}
}
