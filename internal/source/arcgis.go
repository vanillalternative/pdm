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

// ArcGISFeatureConfig describes an ArcGIS REST feature layer query that
// returns GeoJSON geometry clipped server-side by the query bounding box.
type ArcGISFeatureConfig struct {
	LayerURL  string
	OutFields []string
	Where     string
	Meta      model.Source
}

// ArcGISFeatures loads feature geometry from an ArcGIS REST FeatureServer or
// MapServer layer. The layer must support GeoJSON query responses.
func ArcGISFeatures(cfg ArcGISFeatureConfig, opts Options) Loader {
	return func(ctx context.Context) (Loaded, error) {
		if !opts.Live {
			return Loaded{}, fmt.Errorf("live disabled")
		}
		if opts.BBox == nil || opts.BBox.empty() {
			return Loaded{}, fmt.Errorf("ArcGIS feature query requires a bbox")
		}
		b := opts.BBox
		env, _ := json.Marshal(map[string]any{
			"xmin": b.MinLon, "ymin": b.MinLat,
			"xmax": b.MaxLon, "ymax": b.MaxLat,
			"spatialReference": map[string]any{"wkid": 4326},
		})
		q := url.Values{}
		q.Set("f", "geojson")
		q.Set("geometry", string(env))
		q.Set("geometryType", "esriGeometryEnvelope")
		q.Set("inSR", "4326")
		q.Set("outSR", "4326")
		q.Set("spatialRel", "esriSpatialRelIntersects")
		q.Set("returnGeometry", "true")
		q.Set("where", cfg.Where)
		if cfg.Where == "" {
			q.Set("where", "1=1")
		}
		if len(cfg.OutFields) == 0 {
			q.Set("outFields", "*")
		} else {
			q.Set("outFields", strings.Join(cfg.OutFields, ","))
		}
		reqURL := cfg.LayerURL + "/query?" + q.Encode()
		data, fetchedAt, fromCache, err := fetch(ctx, opts, reqURL)
		if err != nil {
			return Loaded{}, err
		}
		var arcErr struct {
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &arcErr); err == nil && arcErr.Error != nil {
			return Loaded{}, fmt.Errorf("ArcGIS feature query %s: %s", trim(reqURL), arcErr.Error.Message)
		}
		feats, err := spatial.LoadFeatureCollection(data)
		if err != nil {
			return Loaded{}, fmt.Errorf("ArcGIS feature query %s: %w", trim(reqURL), err)
		}
		cachePut(opts, reqURL, data, fetchedAt, fromCache)
		m := cfg.Meta
		m.URL = cfg.LayerURL
		m.Provenance = provenanceForFetch(fromCache)
		m.RetrievedAt = &fetchedAt
		return Loaded{Features: feats, Source: m}, nil
	}
}
