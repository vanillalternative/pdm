// Package spatial wraps the simplefeatures geometry engine with the small set
// of operations this tool needs: loading GeoJSON layers and inputs, testing
// point containment, and computing intersection coverage (union of clips so
// overlapping features in a layer are never double-counted).
//
// Public planning data is frequently not OGC-valid (self-touching rings, etc.),
// so layer geometries are parsed with validation disabled and every overlay
// operation is guarded so a single pathological feature degrades gracefully
// instead of aborting the whole query.
//
// All geometry here is in WGS84 lon/lat (X=lon, Y=lat). Area is only ever
// computed after projecting to metres via the crs package; topological
// operations (intersects/intersection/union) are valid in lon/lat at the
// spatial extents of a single municipality.
package spatial

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/peterstace/simplefeatures/geom"
)

// Feature is a geometry plus its free-form properties.
type Feature struct {
	Geometry geom.Geometry
	Props    map[string]any
}

// Point builds a point geometry from a WGS84 lon/lat pair.
func Point(lon, lat float64) geom.Geometry {
	return geom.XY{X: lon, Y: lat}.AsPoint().AsGeometry()
}

// Prop returns the first present, non-empty property among keys (case- and
// space-insensitive match), rendered as a trimmed string.
func (f Feature) Prop(keys ...string) string {
	for _, k := range keys {
		for pk, pv := range f.Props {
			if normKey(pk) == normKey(k) {
				if s := stringify(pv); s != "" {
					return s
				}
			}
		}
	}
	return ""
}

func normKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func stringify(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		// Collapse internal whitespace runs; source data often has stray double
		// spaces or missing/hairline spacing around dashes.
		return strings.Join(strings.Fields(t), " ")
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", t))
	}
}

// rawFeature mirrors the parts of a GeoJSON Feature we need.
type rawFeature struct {
	Geometry   json.RawMessage `json:"geometry"`
	Properties map[string]any  `json:"properties"`
}

// LoadFeatureCollection parses a GeoJSON FeatureCollection resiliently:
// geometries are parsed with validation disabled, and features that cannot be
// parsed at all are skipped rather than failing the whole load.
func LoadFeatureCollection(data []byte) ([]Feature, error) {
	var fc struct {
		Features []rawFeature `json:"features"`
	}
	if err := json.Unmarshal(data, &fc); err != nil {
		return nil, fmt.Errorf("parse feature collection: %w", err)
	}
	out := make([]Feature, 0, len(fc.Features))
	for _, rf := range fc.Features {
		if len(rf.Geometry) == 0 || string(rf.Geometry) == "null" {
			continue
		}
		g, err := geom.UnmarshalGeoJSON(rf.Geometry, geom.NoValidate{})
		if err != nil || g.IsEmpty() {
			continue
		}
		out = append(out, Feature{Geometry: g, Props: rf.Properties})
	}
	return out, nil
}

// LoadFeatureCollectionFile reads and parses a GeoJSON FeatureCollection file.
func LoadFeatureCollectionFile(path string) ([]Feature, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return LoadFeatureCollection(data)
}

// LoadInputGeometry reads a user-supplied GeoJSON file that may be a bare
// geometry, a Feature, or a FeatureCollection, and returns a single geometry
// suitable for analysis (multiple features are unioned).
func LoadInputGeometry(path string) (geom.Geometry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return geom.Geometry{}, err
	}
	return ParseInputGeometry(data)
}

// ParseInputGeometry is LoadInputGeometry for in-memory bytes.
func ParseInputGeometry(data []byte) (geom.Geometry, error) {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return geom.Geometry{}, fmt.Errorf("invalid GeoJSON: %w", err)
	}
	switch probe.Type {
	case "FeatureCollection":
		feats, err := LoadFeatureCollection(data)
		if err != nil {
			return geom.Geometry{}, err
		}
		if len(feats) == 0 {
			return geom.Geometry{}, fmt.Errorf("feature collection has no usable geometries")
		}
		geoms := make([]geom.Geometry, 0, len(feats))
		for _, f := range feats {
			geoms = append(geoms, f.Geometry)
		}
		return UnionAll(geoms)
	case "Feature":
		var rf rawFeature
		if err := json.Unmarshal(data, &rf); err != nil {
			return geom.Geometry{}, fmt.Errorf("parse feature: %w", err)
		}
		g, err := geom.UnmarshalGeoJSON(rf.Geometry, geom.NoValidate{})
		if err != nil {
			return geom.Geometry{}, fmt.Errorf("parse feature geometry: %w", err)
		}
		if g.IsEmpty() {
			return geom.Geometry{}, fmt.Errorf("feature has empty geometry")
		}
		return g, nil
	case "":
		return geom.Geometry{}, fmt.Errorf("GeoJSON missing 'type' field")
	default:
		g, err := geom.UnmarshalGeoJSON(data, geom.NoValidate{})
		if err != nil {
			return geom.Geometry{}, fmt.Errorf("parse geometry: %w", err)
		}
		return g, nil
	}
}

// UnionAll reduces geometries to their union, tolerating individual failures.
func UnionAll(geoms []geom.Geometry) (geom.Geometry, error) {
	var acc geom.Geometry
	first := true
	for _, g := range geoms {
		if g.IsEmpty() {
			continue
		}
		if first {
			acc = g
			first = false
			continue
		}
		u, err := safeUnion(acc, g)
		if err != nil {
			continue // tolerate a pathological geometry
		}
		acc = u
	}
	if first {
		return geom.Geometry{}, fmt.Errorf("no non-empty geometries")
	}
	return acc, nil
}

// Intersects reports whether a and b share any point (guarded against panics on
// malformed geometry).
func Intersects(a, b geom.Geometry) (result bool) {
	defer func() {
		if recover() != nil {
			result = false
		}
	}()
	return geom.Intersects(a, b)
}

// Coverage returns the union of (subject ∩ f) over all features — the part of
// subject actually covered by the feature set, with overlaps collapsed.
// Features whose intersection fails are skipped. Returns an empty geometry when
// there is no overlap.
func Coverage(subject geom.Geometry, feats []Feature) (geom.Geometry, error) {
	var clips []geom.Geometry
	for _, f := range feats {
		if !Intersects(subject, f.Geometry) {
			continue
		}
		clip, err := safeIntersection(subject, f.Geometry)
		if err != nil {
			continue // tolerate invalid feature
		}
		if !clip.IsEmpty() {
			clips = append(clips, clip)
		}
	}
	if len(clips) == 0 {
		return geom.Geometry{}, nil
	}
	return UnionAll(clips)
}

// safeIntersection / safeUnion guard the overlay ops against panics that
// simplefeatures can raise on non-OGC-valid input.
func safeIntersection(a, b geom.Geometry) (g geom.Geometry, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("intersection panicked: %v", r)
		}
	}()
	return geom.Intersection(a, b)
}

func safeUnion(a, b geom.Geometry) (g geom.Geometry, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("union panicked: %v", r)
		}
	}()
	return geom.Union(a, b)
}

// Intersection is the guarded intersection used by callers outside Coverage.
func Intersection(a, b geom.Geometry) (geom.Geometry, error) {
	return safeIntersection(a, b)
}
