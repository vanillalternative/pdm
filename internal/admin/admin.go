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

// Municipality identifies a resolved municipality (concelho) from CAOP.
type Municipality struct {
	Name     string
	Code     string // CAOP dtmn code (distrito+município, e.g. "1418")
	District string // distrito_ilha
}

// Property keys that may hold each municipality attribute across CAOP releases
// and derived datasets.
var (
	nameFields = []string{
		"Concelho", "concelho", "MUNICIPIO", "Municipio", "municipio",
		"NAME_2", "name", "Concelho_D", "DESIGNCONC", "des_simpli",
	}
	codeFields     = []string{"dtmn", "DTMN", "dico", "DICO", "dtcc"}
	districtFields = []string{"distrito_ilha", "Distrito", "distrito", "NAME_1"}
)

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

// Count returns the number of municipalities in the boundary dataset.
func (r *Resolver) Count() int { return len(r.features) }

func municipality(f spatial.Feature) Municipality {
	m := Municipality{
		Name:     f.Prop(nameFields...),
		Code:     f.Prop(codeFields...),
		District: f.Prop(districtFields...),
	}
	if m.Name == "" {
		m.Name = "(unknown municipality)"
	}
	return m
}

// ResolvePoint returns the municipality containing the point, or ok=false if
// the point falls outside all known boundaries.
func (r *Resolver) ResolvePoint(lon, lat float64) (Municipality, bool) {
	pt := spatial.Point(lon, lat)
	for _, f := range r.features {
		if spatial.Intersects(pt, f.Geometry) {
			return municipality(f), true
		}
	}
	return Municipality{}, false
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

// FreguesiaResolver answers "which freguesia is here?" from CAOP freguesia
// boundaries. It only labels results (the freguesia has no planning layers of
// its own), so it tolerates heavily simplified geometry.
type FreguesiaResolver struct {
	features []spatial.Feature
}

var freguesiaNameFields = []string{"freguesia", "Freguesia", "FREGUESIA", "des_simpli"}

// NewFreguesiaResolver builds a FreguesiaResolver from a GeoJSON
// FeatureCollection of freguesia boundaries.
func NewFreguesiaResolver(boundaries []byte) (*FreguesiaResolver, error) {
	feats, err := spatial.LoadFeatureCollection(boundaries)
	if err != nil {
		return nil, fmt.Errorf("load freguesia boundaries: %w", err)
	}
	if len(feats) == 0 {
		return nil, fmt.Errorf("freguesia dataset is empty")
	}
	return &FreguesiaResolver{features: feats}, nil
}

// ResolvePoint returns the freguesia name containing the point, or ok=false.
func (r *FreguesiaResolver) ResolvePoint(lon, lat float64) (string, bool) {
	pt := spatial.Point(lon, lat)
	for _, f := range r.features {
		if spatial.Intersects(pt, f.Geometry) {
			if name := f.Prop(freguesiaNameFields...); name != "" {
				return name, true
			}
		}
	}
	return "", false
}

// ResolvePolygon returns the freguesia with the greatest overlap with the
// input geometry, or ok=false if there is no overlap at all.
func (r *FreguesiaResolver) ResolvePolygon(g geom.Geometry) (string, bool) {
	bestName, bestArea := "", 0.0
	for _, f := range r.features {
		if !spatial.Intersects(g, f.Geometry) {
			continue
		}
		clip, err := spatial.Intersection(g, f.Geometry)
		if err != nil || clip.IsEmpty() {
			continue
		}
		if area := crs.AreaM2(clip); area > bestArea {
			if name := f.Prop(freguesiaNameFields...); name != "" {
				bestName, bestArea = name, area
			}
		}
	}
	return bestName, bestName != ""
}

// Overlap describes how much of an input polygon falls in a municipality.
type Overlap struct {
	Municipality Municipality
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
		overlaps = append(overlaps, Overlap{Municipality: municipality(f), AreaM2: area, Percent: pct})
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
