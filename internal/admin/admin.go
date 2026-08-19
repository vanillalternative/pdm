// Package admin resolves a coordinate or polygon to the Portuguese municipality
// (concelho) that contains it, using official administrative boundaries (CAOP)
// supplied as a GeoJSON FeatureCollection.
package admin

import (
	"fmt"
	"sort"

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

// Info is Municipality plus geometry-derived data for listings/pages.
type Info struct {
	Municipality
	CentroidLon, CentroidLat float64
	BBox                     [4]float64 // minLon, minLat, maxLon, maxLat (WGS84)
}

// List returns every municipality in the boundary dataset, code-ordered.
func (r *Resolver) List() []Info {
	out := make([]Info, 0, len(r.features))
	for _, f := range r.features {
		info := Info{Municipality: municipality(f)}
		if xy, ok := f.Geometry.Centroid().XY(); ok {
			info.CentroidLon, info.CentroidLat = xy.X, xy.Y
		}
		if min, max, ok := f.Geometry.Envelope().MinMaxXYs(); ok {
			info.BBox = [4]float64{min.X, min.Y, max.X, max.Y}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

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
