// Package query is the spatial query engine: it resolves a location to a
// municipality, loads that municipality's planning layers via its adapter, and
// intersects the input against them to produce a structured result.
package query

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/bernardosimoes/pdm/internal/adapter"
	"github.com/bernardosimoes/pdm/internal/admin"
	"github.com/bernardosimoes/pdm/internal/crs"
	"github.com/bernardosimoes/pdm/internal/model"
	"github.com/bernardosimoes/pdm/internal/registry"
	"github.com/bernardosimoes/pdm/internal/source"
	"github.com/bernardosimoes/pdm/internal/spatial"
	"github.com/peterstace/simplefeatures/geom"
)

// Engine performs planning queries.
type Engine struct {
	resolver *admin.Resolver
	opts     source.Options
	now      func() time.Time
}

// New builds an Engine from a municipality resolver and runtime options.
func New(resolver *admin.Resolver, opts source.Options) *Engine {
	return &Engine{resolver: resolver, opts: opts, now: time.Now}
}

// Point answers a coordinate query.
func (e *Engine) Point(ctx context.Context, lon, lat float64) (*model.PointResult, error) {
	res := &model.PointResult{
		Input:       model.Coordinate{Lat: lat, Lon: lon},
		Zoning:      []model.ZoningHit{},
		Constraints: []model.ConstraintHit{},
		Sources:     []model.Source{},
		Disclaimer:  model.Disclaimer,
		GeneratedAt: e.now(),
	}

	muni, ok := e.resolver.ResolvePoint(lon, lat)
	if !ok {
		res.Municipality = "(undetermined)"
		res.Confidence = model.ConfidenceLow
		res.Notes = append(res.Notes,
			"Coordinate is outside the bundled administrative coverage; municipality could not be determined.")
		return res, nil
	}
	res.Municipality = muni

	ad, ok := registry.Lookup(muni)
	if !ok {
		res.Supported = false
		res.Confidence = model.ConfidenceLow
		res.Notes = append(res.Notes, unsupportedNote(muni))
		return res, nil
	}
	res.Supported = true
	plan := ad.Plan()
	res.Plan = &plan

	opts := e.opts
	bb := pointBBox(lon, lat, 0.03)
	opts.BBox = &bb

	pt := spatial.Point(lon, lat)
	collector := newSourceSet()
	confidence := ad.BaseConfidence()
	zoneSeen := map[string]bool{}

	for _, layer := range ad.Layers(opts) {
		loaded, err := layer.Loader(ctx)
		if err != nil {
			res.Notes = append(res.Notes, fmt.Sprintf("Layer %q unavailable: %v", layer.Title, err))
			confidence = downgrade(confidence)
			continue
		}
		collector.add(loaded.Source)
		if isSample(loaded.Source) {
			confidence = model.ConfidenceLow
		}

		switch layer.Kind {
		case adapter.KindZoning:
			for _, f := range loaded.Features {
				if !spatial.Intersects(pt, f.Geometry) {
					continue
				}
				c := classify(layer, f)
				if zoneSeen[c.Label] { // overlapping polygons can repeat a class
					continue
				}
				zoneSeen[c.Label] = true
				res.Zoning = append(res.Zoning, model.ZoningHit{
					Class:    c.Class,
					Subclass: c.Subclass,
					Label:    c.Label,
					RawCode:  c.RawCode,
					Layer:    layer.ID,
				})
			}
		case adapter.KindConstraint:
			hit := model.ConstraintHit{
				Type:  layer.Constraint,
				Label: layer.Title,
				Layer: layer.ID,
			}
			for _, f := range loaded.Features {
				if spatial.Intersects(pt, f.Geometry) {
					hit.Present = true
					if layer.Detail != nil {
						if d := layer.Detail(f); d != "" {
							hit.Detail = d
						}
					}
					break
				}
			}
			res.Constraints = append(res.Constraints, hit)
		}
	}

	if len(res.Zoning) == 0 {
		res.Notes = append(res.Notes, "No zoning category matched the coordinate in the available layers.")
		confidence = downgrade(confidence)
	}
	res.Sources = collector.list()
	res.Confidence = confidence
	return res, nil
}

// Polygon answers a polygon (parcel) query.
func (e *Engine) Polygon(ctx context.Context, g geom.Geometry) (*model.PolygonResult, error) {
	res := &model.PolygonResult{
		Zoning:      []model.ZoningHit{},
		Constraints: []model.ConstraintHit{},
		Sources:     []model.Source{},
		Disclaimer:  model.Disclaimer,
		GeneratedAt: e.now(),
	}
	best, overlaps, ok := e.resolver.ResolvePolygon(g)
	if !ok {
		res.AnalysedAreaM2 = crs.AreaM2(g)
		res.Municipality = "(undetermined)"
		res.Confidence = model.ConfidenceLow
		res.Notes = append(res.Notes,
			"Polygon does not overlap the bundled administrative coverage; municipality could not be determined.")
		return res, nil
	}
	res.Municipality = best.Municipality
	// Analyse only the portion inside the resolved municipality, so zoning and
	// constraint percentages are shares of the analysed area — not of a parcel
	// that may straddle a boundary into unanalysed municipalities.
	total := best.AreaM2
	res.AnalysedAreaM2 = total
	if len(overlaps) > 1 {
		res.Notes = append(res.Notes, straddleNote(best, overlaps))
	}

	ad, ok := registry.Lookup(best.Municipality)
	if !ok {
		res.Supported = false
		res.Confidence = model.ConfidenceLow
		res.Notes = append(res.Notes, unsupportedNote(best.Municipality))
		return res, nil
	}
	res.Supported = true
	plan := ad.Plan()
	res.Plan = &plan

	opts := e.opts
	bb := geomBBox(g, 0.005)
	opts.BBox = &bb

	collector := newSourceSet()
	confidence := ad.BaseConfidence()

	for _, layer := range ad.Layers(opts) {
		loaded, err := layer.Loader(ctx)
		if err != nil {
			res.Notes = append(res.Notes, fmt.Sprintf("Layer %q unavailable: %v", layer.Title, err))
			confidence = downgrade(confidence)
			continue
		}
		collector.add(loaded.Source)
		if isSample(loaded.Source) {
			confidence = model.ConfidenceLow
		}

		switch layer.Kind {
		case adapter.KindZoning:
			res.Zoning = append(res.Zoning, zoningBreakdown(g, total, layer, loaded.Features)...)
		case adapter.KindConstraint:
			hit := constraintCoverage(g, total, layer, loaded.Features)
			res.Constraints = append(res.Constraints, hit)
		}
	}

	if len(res.Zoning) == 0 {
		res.Notes = append(res.Notes, "No zoning category matched the parcel in the available layers.")
		confidence = downgrade(confidence)
	}

	sort.SliceStable(res.Zoning, func(i, j int) bool { return res.Zoning[i].AreaM2 > res.Zoning[j].AreaM2 })
	sort.SliceStable(res.Constraints, func(i, j int) bool { return res.Constraints[i].AreaM2 > res.Constraints[j].AreaM2 })

	res.Sources = collector.list()
	res.Confidence = confidence
	return res, nil
}

// zoningBreakdown groups intersecting zoning features by classification label
// and returns one hit per category with area and percentage of the parcel.
func zoningBreakdown(g geom.Geometry, total float64, layer adapter.Layer, feats []spatial.Feature) []model.ZoningHit {
	type group struct {
		c     adapter.Classification
		feats []spatial.Feature
	}
	order := []string{}
	groups := map[string]*group{}
	for _, f := range feats {
		if !spatial.Intersects(g, f.Geometry) {
			continue
		}
		c := classify(layer, f)
		key := c.Label
		if groups[key] == nil {
			groups[key] = &group{c: c}
			order = append(order, key)
		}
		groups[key].feats = append(groups[key].feats, f)
	}
	var out []model.ZoningHit
	for _, key := range order {
		grp := groups[key]
		cov, err := spatial.Coverage(g, grp.feats)
		if err != nil || cov.IsEmpty() {
			continue
		}
		area := crs.AreaM2(cov)
		if area <= 0 {
			continue
		}
		out = append(out, model.ZoningHit{
			Class:    grp.c.Class,
			Subclass: grp.c.Subclass,
			Label:    grp.c.Label,
			RawCode:  grp.c.RawCode,
			Layer:    layer.ID,
			AreaM2:   area,
			Percent:  percent(area, total),
		})
	}
	return out
}

// constraintCoverage computes the parcel area covered by a constraint layer.
func constraintCoverage(g geom.Geometry, total float64, layer adapter.Layer, feats []spatial.Feature) model.ConstraintHit {
	hit := model.ConstraintHit{Type: layer.Constraint, Label: layer.Title, Layer: layer.ID}
	cov, err := spatial.Coverage(g, feats)
	if err != nil || cov.IsEmpty() {
		return hit
	}
	area := crs.AreaM2(cov)
	if area <= 0 {
		return hit
	}
	hit.Present = true
	hit.AreaM2 = area
	hit.Percent = percent(area, total)
	return hit
}

func classify(layer adapter.Layer, f spatial.Feature) adapter.Classification {
	if layer.Classify != nil {
		return layer.Classify(f)
	}
	raw := f.Prop("classe", "categoria", "class", "label")
	return adapter.Classification{Class: raw, Label: raw, RawCode: raw}
}

func percent(area, total float64) float64 {
	if total <= 0 {
		return 0
	}
	return area / total * 100
}

func pointBBox(lon, lat, pad float64) source.BBox {
	return source.BBox{MinLon: lon - pad, MinLat: lat - pad, MaxLon: lon + pad, MaxLat: lat + pad}
}

func geomBBox(g geom.Geometry, pad float64) source.BBox {
	min, max, ok := g.Envelope().MinMaxXYs()
	if !ok {
		return source.BBox{}
	}
	return source.BBox{
		MinLon: min.X - pad, MinLat: min.Y - pad,
		MaxLon: max.X + pad, MaxLat: max.Y + pad,
	}
}

func unsupportedNote(muni string) string {
	sup := registry.Supported()
	return fmt.Sprintf("Municipality %q detected but not yet supported. Supported: %v.", muni, sup)
}

func straddleNote(best admin.Overlap, overlaps []admin.Overlap) string {
	return fmt.Sprintf(
		"Polygon straddles %d municipalities; analysing the majority one (%s, %.1f%% of area). "+
			"Results for the remainder are not included.",
		len(overlaps), best.Municipality, best.Percent)
}

func isSample(s model.Source) bool { return s.Provenance == model.ProvenanceSample }

func downgrade(c model.Confidence) model.Confidence {
	switch c {
	case model.ConfidenceHigh:
		return model.ConfidenceMedium
	case model.ConfidenceMedium:
		return model.ConfidenceLow
	default:
		return model.ConfidenceLow
	}
}

// sourceSet dedupes sources by name+layer while preserving insertion order.
type sourceSet struct {
	seen  map[string]bool
	items []model.Source
}

func newSourceSet() *sourceSet { return &sourceSet{seen: map[string]bool{}} }

func (s *sourceSet) add(src model.Source) {
	if src.Name == "" && src.Layer == "" {
		return
	}
	key := src.Name + "|" + src.Layer
	if s.seen[key] {
		return
	}
	s.seen[key] = true
	s.items = append(s.items, src)
}

func (s *sourceSet) list() []model.Source {
	if s.items == nil {
		return []model.Source{}
	}
	return s.items
}
