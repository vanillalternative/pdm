package source

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/bernardosimoes/pdm/internal/model"
	"github.com/bernardosimoes/pdm/internal/spatial"
)

// WMSFeatureInfoConfig describes a WMS GetFeatureInfo request used as an
// attribute probe at the centre of the query envelope.
type WMSFeatureInfoConfig struct {
	BaseURL string // service endpoint, including stable query params such as map=
	Layer   string
	Meta    model.Source
}

// WMSFeatureInfo returns a loader that asks a WMS layer for the attributes at the
// centre of opts.BBox. Returned features use a synthetic point geometry at that
// centre because GetFeatureInfo does not expose source polygons.
func WMSFeatureInfo(cfg WMSFeatureInfoConfig, opts Options) Loader {
	return func(ctx context.Context) (Loaded, error) {
		if !opts.Live {
			return Loaded{}, fmt.Errorf("live disabled")
		}
		if opts.BBox == nil || opts.BBox.empty() {
			return Loaded{}, fmt.Errorf("WMS GetFeatureInfo requires a query bbox")
		}
		reqURL, lon, lat := wmsFeatureInfoURL(cfg.BaseURL, cfg.Layer, *opts.BBox)
		data, fetchedAt, fromCache, err := fetch(ctx, opts, reqURL)
		if err != nil {
			return Loaded{}, err
		}
		props, err := parseFeatureInfoProperties(data, reqURL)
		if err != nil {
			return Loaded{}, err
		}
		cachePut(opts, reqURL, data, fetchedAt, fromCache)
		feats := make([]spatial.Feature, 0, len(props))
		for _, p := range props {
			feats = append(feats, spatial.Feature{Geometry: spatial.Point(lon, lat), Props: p})
		}
		m := cfg.Meta
		m.URL = reqURL
		m.Provenance = provenanceForFetch(fromCache)
		m.RetrievedAt = &fetchedAt
		return Loaded{Features: feats, Source: m}, nil
	}
}

// WMSFeatureInfoProbe returns a constraint prober backed by WMS GetFeatureInfo.
func WMSFeatureInfoProbe(cfg WMSFeatureInfoConfig, opts Options, interpret func([]spatial.Feature) Interpretation) Prober {
	return func(ctx context.Context, subject BBox) (Probed, error) {
		reqURL, _, _ := wmsFeatureInfoURL(cfg.BaseURL, cfg.Layer, subject)
		data, fetchedAt, fromCache, err := fetch(ctx, opts, reqURL)
		if err != nil {
			return Probed{}, err
		}
		props, err := parseFeatureInfoProperties(data, reqURL)
		if err != nil {
			return Probed{}, err
		}
		cachePut(opts, reqURL, data, fetchedAt, fromCache)
		feats := make([]spatial.Feature, 0, len(props))
		for _, p := range props {
			feats = append(feats, spatial.Feature{Props: p})
		}
		if interpret == nil {
			interpret = defaultInterpret
		}
		r := interpret(feats)
		m := cfg.Meta
		m.URL = reqURL
		m.Provenance = provenanceForFetch(fromCache)
		m.RetrievedAt = &fetchedAt
		return Probed{Present: r.Present, Detail: r.Detail, Note: r.Note, Source: m}, nil
	}
}

func wmsFeatureInfoURL(base, layer string, bbox BBox) (string, float64, float64) {
	lon := (bbox.MinLon + bbox.MaxLon) / 2
	lat := (bbox.MinLat + bbox.MaxLat) / 2
	q := url.Values{}
	q.Set("SERVICE", "WMS")
	q.Set("VERSION", "1.3.0")
	q.Set("REQUEST", "GetFeatureInfo")
	q.Set("LAYERS", layer)
	q.Set("QUERY_LAYERS", layer)
	q.Set("CRS", "EPSG:4326")
	// WMS 1.3.0 uses latitude,longitude axis order for EPSG:4326.
	q.Set("BBOX", fmt.Sprintf("%g,%g,%g,%g", bbox.MinLat, bbox.MinLon, bbox.MaxLat, bbox.MaxLon))
	q.Set("WIDTH", "3")
	q.Set("HEIGHT", "3")
	q.Set("I", "1")
	q.Set("J", "1")
	q.Set("INFO_FORMAT", "application/json")
	return appendQuery(base, q), lon, lat
}

func appendQuery(base string, q url.Values) string {
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + q.Encode()
}

func parseFeatureInfoProperties(data []byte, reqURL string) ([]map[string]any, error) {
	var doc struct {
		Features *[]struct {
			Properties map[string]any `json:"properties"`
		} `json:"features"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("WMS GetFeatureInfo %s: %w", trim(reqURL), err)
	}
	if doc.Features == nil {
		return nil, fmt.Errorf("WMS GetFeatureInfo %s: response has no features array", trim(reqURL))
	}
	out := make([]map[string]any, 0, len(*doc.Features))
	for _, f := range *doc.Features {
		if len(f.Properties) > 0 {
			out = append(out, f.Properties)
		}
	}
	return out, nil
}
