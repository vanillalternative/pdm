// Package admin resolves a coordinate or polygon to the Portuguese municipality
// (concelho) that contains it, using official administrative boundaries (CAOP)
// supplied as a GeoJSON FeatureCollection.
package admin

import (
	"fmt"

	"github.com/bernardosimoes/pdm/internal/crs"
	"github.com/bernardosimoes/pdm/internal/spatial"
	"github.com/peterstace/simplefeatures/geom"
)

// nameFields lists the property keys that may hold a municipality name across
// CAOP releases and derived datasets.
var nameFields = []string{
	"Concelho", "concelho", "MUNICIPIO", "Municipio", "municipio",
	"NAME_2", "name", "Concelho_D", "DESIGNCONC", "des_simpli",
}

// Resolver answers "which municipality is here?" from boundary polygons.
type Resolver struct {
	features []spatial.Feature
}

// NewResolver builds a Resolver from a GeoJSON FeatureCollection of municipal
// boundaries.
func NewResolver(boundaries []byte) (*Resolver, error) {
	feats, err := spatial.LoadFeatureCollection(boundaries)
	if err != nil {
		return nil, fmt.Errorf("load boundaries: %w", err)
	}
	if len(feats) == 0 {
		return nil, fmt.Errorf("boundaries dataset is empty")
	}
	return &Resolver{features: feats}, nil
}

func name(f spatial.Feature) string {
	if n := f.Prop(nameFields...); n != "" {
		return n
	}
	return "(unknown municipality)"
}

// ResolvePoint returns the municipality name containing the point, or ok=false
// if the point falls outside all known boundaries.
func (r *Resolver) ResolvePoint(lon, lat float64) (string, bool) {
	pt := spatial.Point(lon, lat)
	for _, f := range r.features {
		if spatial.Intersects(pt, f.Geometry) {
			return name(f), true
		}
	}
	return "", false
}

// BoundaryAt returns the boundary geometry of the municipality containing the
// point (for map rendering), or ok=false if the point is outside all boundaries.
func (r *Resolver) BoundaryAt(lon, lat float64) (geom.Geometry, bool) {
	pt := spatial.Point(lon, lat)
	for _, f := range r.features {
		if spatial.Intersects(pt, f.Geometry) {
			return f.Geometry, true
		}
	}
	return geom.Geometry{}, false
}

// Overlap describes how much of an input polygon falls in a municipality.
type Overlap struct {
	Municipality string
	AreaM2       float64
	Percent      float64
}

// ResolvePolygon returns the municipality with the greatest overlap with the
// input geometry, plus the full list of overlaps (so callers can warn about
// parcels that straddle boundaries). ok=false if there is no overlap at all.
func (r *Resolver) ResolvePolygon(g geom.Geometry) (Overlap, []Overlap, bool) {
	total := crs.AreaM2(g)
	var overlaps []Overlap
	for _, f := range r.features {
		if !spatial.Intersects(g, f.Geometry) {
			continue
		}
		clip, err := spatial.Intersection(g, f.Geometry)
		if err != nil || clip.IsEmpty() {
			continue
		}
		area := crs.AreaM2(clip)
		if area <= 0 {
			continue
		}
		pct := 0.0
		if total > 0 {
			pct = area / total * 100
		}
		overlaps = append(overlaps, Overlap{Municipality: name(f), AreaM2: area, Percent: pct})
	}
	if len(overlaps) == 0 {
		return Overlap{}, nil, false
	}
	best := overlaps[0]
	for _, o := range overlaps[1:] {
		if o.AreaM2 > best.AreaM2 {
			best = o
		}
	}
	return best, overlaps, true
}
