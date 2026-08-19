// Server-side presence probes. Some national layers (REN delimitations,
// protected areas, Natura 2000 sites) are whole-municipality or whole-region
// multipolygons far too large to download per query. Instead of loading their
// geometries, a Prober asks the source service what intersects the subject —
// a tiny bbox around the queried point (OGC API Features honours bbox with a
// true geometry intersection and skipGeometry=true keeps responses small) or a
// point/envelope geometry query (ArcGIS REST evaluates intersection and
// distance server-side with returnGeometry=false). The answer carries the
// matched features' attributes, never their geometries.
package source

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/bernardosimoes/pdm/internal/model"
	"github.com/bernardosimoes/pdm/internal/spatial"
)

// Probed is the outcome of a server-side presence probe.
type Probed struct {
	Present bool
	Unknown bool   // the source has no data for this municipality — absence of data, not absence of the constraint
	Detail  string // short description of what was hit (or why the answer is unknown)
	Note    string // honest caveat to surface with the result
	Source  model.Source
}

// Prober evaluates a constraint against the subject envelope, server-side.
type Prober func(ctx context.Context, subject BBox) (Probed, error)

// Interpretation is the layer-specific reading of a probe's matched features.
type Interpretation struct {
	Present bool
	Detail  string
	Note    string
}

// OGCProbeConfig describes an OGC API Features presence probe.
type OGCProbeConfig struct {
	ItemsURL string            // collection items endpoint
	Params   map[string]string // attribute filters applied to the probe (e.g. dtcc)

	// Existence, when non-nil, is checked once (limit=1, skipGeometry, no bbox)
	// to distinguish "the subject misses every feature" from "the dataset has no
	// features for this municipality at all". On a failed existence check the
	// probe returns Unknown=true with AbsenceDetail/AbsenceNote.
	Existence     map[string]string
	AbsenceDetail string
	AbsenceNote   string

	// Interpret reads the matched (geometryless) features into a result. When
	// nil, any match is Present with the first feature's "designacao"/"tipologia"
	// as detail.
	Interpret func(hits []spatial.Feature) Interpretation

	Meta model.Source
}

// OGCProbe builds a Prober over an OGC API Features collection.
func OGCProbe(cfg OGCProbeConfig, opts Options) Prober {
	return func(ctx context.Context, subject BBox) (Probed, error) {
		if cfg.Existence != nil {
			n, src, err := ogcCount(ctx, opts, cfg.ItemsURL, cfg.Existence)
			if err != nil {
				return Probed{}, err
			}
			if n == 0 {
				return Probed{
					Unknown: true,
					Detail:  cfg.AbsenceDetail,
					Note:    cfg.AbsenceNote,
					Source:  src,
				}, nil
			}
		}
		hits, src, err := ogcHits(ctx, opts, cfg.ItemsURL, cfg.Params, &subject)
		if err != nil {
			return Probed{}, err
		}
		src.Name, src.Layer = cfg.Meta.Name, cfg.Meta.Layer
		interp := cfg.Interpret
		if interp == nil {
			interp = defaultInterpret
		}
		r := interp(hits)
		return Probed{Present: r.Present, Detail: r.Detail, Note: r.Note, Source: src}, nil
	}
}

func defaultInterpret(hits []spatial.Feature) Interpretation {
	if len(hits) == 0 {
		return Interpretation{}
	}
	return Interpretation{Present: true, Detail: hits[0].Prop("designacao", "tipologia", "nome")}
}

// ogcHits fetches the matched features' attributes (skipGeometry) for a bbox
// probe (bbox may be nil for attribute-only queries).
func ogcHits(ctx context.Context, opts Options, itemsURL string, params map[string]string, bbox *BBox) ([]spatial.Feature, model.Source, error) {
	q := url.Values{}
	q.Set("f", "json")
	q.Set("limit", "20")
	q.Set("skipGeometry", "true")
	for k, v := range params {
		q.Set(k, v)
	}
	if bbox != nil {
		q.Set("bbox", fmt.Sprintf("%g,%g,%g,%g", bbox.MinLon, bbox.MinLat, bbox.MaxLon, bbox.MaxLat))
	}
	reqURL := itemsURL + "?" + q.Encode()
	data, fetchedAt, fromCache, err := fetch(ctx, opts, reqURL)
	if err != nil {
		return nil, model.Source{}, err
	}
	var doc struct {
		Features []struct {
			Properties map[string]any `json:"properties"`
		} `json:"features"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, model.Source{}, fmt.Errorf("probe %s: %w", trim(reqURL), err)
	}
	cachePut(opts, reqURL, data, fetchedAt, fromCache)
	feats := make([]spatial.Feature, 0, len(doc.Features))
	for _, f := range doc.Features {
		feats = append(feats, spatial.Feature{Props: f.Properties})
	}
	src := model.Source{
		URL:         itemsURL,
		Provenance:  provenanceForFetch(fromCache),
		RetrievedAt: &fetchedAt,
	}
	return feats, src, nil
}

// ogcCount runs an attribute-only existence query and returns numberMatched.
func ogcCount(ctx context.Context, opts Options, itemsURL string, params map[string]string) (int, model.Source, error) {
	q := url.Values{}
	q.Set("f", "json")
	q.Set("limit", "1")
	q.Set("skipGeometry", "true")
	for k, v := range params {
		q.Set(k, v)
	}
	reqURL := itemsURL + "?" + q.Encode()
	data, fetchedAt, fromCache, err := fetch(ctx, opts, reqURL)
	if err != nil {
		return 0, model.Source{}, err
	}
	var doc struct {
		NumberMatched *int `json:"numberMatched"`
		Features      []json.RawMessage
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return 0, model.Source{}, fmt.Errorf("probe count %s: %w", trim(reqURL), err)
	}
	cachePut(opts, reqURL, data, fetchedAt, fromCache)
	src := model.Source{URL: itemsURL, Provenance: provenanceForFetch(fromCache), RetrievedAt: &fetchedAt}
	if doc.NumberMatched != nil {
		return *doc.NumberMatched, src, nil
	}
	return len(doc.Features), src, nil
}

// ArcGISProbeConfig describes an ArcGIS REST layer presence probe.
type ArcGISProbeConfig struct {
	LayerURL  string   // e.g. https://host/rest/services/X/MapServer/0
	OutFields []string // attributes to return (default *)
	DistanceM float64  // when >0, a server-side buffer distance in metres
	Where     string   // optional attribute filter
	Meta      model.Source
}

// ArcGISProbe builds a Prober over an ArcGIS REST layer. The subject envelope
// is passed as an esriGeometryEnvelope (a degenerate envelope behaves as a
// point); intersection — and distance, when configured — are evaluated
// server-side and only attributes come back.
func ArcGISProbe(cfg ArcGISProbeConfig, opts Options) func(ctx context.Context, subject BBox) ([]spatial.Feature, model.Source, error) {
	return func(ctx context.Context, subject BBox) ([]spatial.Feature, model.Source, error) {
		q := url.Values{}
		env := map[string]any{
			"xmin": subject.MinLon, "ymin": subject.MinLat,
			"xmax": subject.MaxLon, "ymax": subject.MaxLat,
			"spatialReference": map[string]any{"wkid": 4326},
		}
		envJSON, _ := json.Marshal(env)
		q.Set("geometry", string(envJSON))
		q.Set("geometryType", "esriGeometryEnvelope")
		q.Set("inSR", "4326")
		q.Set("spatialRel", "esriSpatialRelIntersects")
		q.Set("returnGeometry", "false")
		q.Set("f", "json")
		of := "*"
		if len(cfg.OutFields) > 0 {
			of = strings.Join(cfg.OutFields, ",")
		}
		q.Set("outFields", of)
		if cfg.Where != "" {
			q.Set("where", cfg.Where)
		}
		if cfg.DistanceM > 0 {
			q.Set("distance", strconv.FormatFloat(cfg.DistanceM, 'f', -1, 64))
			q.Set("units", "esriSRUnit_Meter")
		}
		reqURL := cfg.LayerURL + "/query?" + q.Encode()
		data, fetchedAt, fromCache, err := fetch(ctx, opts, reqURL)
		if err != nil {
			return nil, model.Source{}, err
		}
		var doc struct {
			Features []struct {
				Attributes map[string]any `json:"attributes"`
			} `json:"features"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			return nil, model.Source{}, fmt.Errorf("arcgis probe %s: %w", trim(reqURL), err)
		}
		if doc.Error != nil {
			// ArcGIS reports errors with HTTP 200; the response is only cached
			// below on success, so the failure is retried, never replayed.
			return nil, model.Source{}, fmt.Errorf("arcgis probe %s: %s", trim(reqURL), doc.Error.Message)
		}
		cachePut(opts, reqURL, data, fetchedAt, fromCache)
		feats := make([]spatial.Feature, 0, len(doc.Features))
		for _, f := range doc.Features {
			feats = append(feats, spatial.Feature{Props: f.Attributes})
		}
		src := cfg.Meta
		src.URL = cfg.LayerURL
		src.Provenance = provenanceForFetch(fromCache)
		src.RetrievedAt = &fetchedAt
		return feats, src, nil
	}
}
