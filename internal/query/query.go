// Package query is the spatial query engine: it resolves a location to a
// municipality, loads that municipality's planning layers via its adapter, and
// intersects the input against them to produce a structured result.
package query

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/bernardosimoes/pdm/internal/adapter"
	"github.com/bernardosimoes/pdm/internal/admin"
	"github.com/bernardosimoes/pdm/internal/crs"
	"github.com/bernardosimoes/pdm/internal/mapview"
	"github.com/bernardosimoes/pdm/internal/model"
	"github.com/bernardosimoes/pdm/internal/registry"
	"github.com/bernardosimoes/pdm/internal/source"
	"github.com/bernardosimoes/pdm/internal/spatial"
	"github.com/peterstace/simplefeatures/geom"
)

// Engine performs planning queries.
type Engine struct {
	resolver   *admin.Resolver
	freguesias *admin.FreguesiaResolver // optional, labels results
	opts       source.Options
	now        func() time.Time
}

// New builds an Engine from a municipality resolver and runtime options.
func New(resolver *admin.Resolver, opts source.Options) *Engine {
	return &Engine{resolver: resolver, opts: opts, now: time.Now}
}

// SetFreguesias attaches an optional freguesia resolver used to label results
// with the freguesia name.
func (e *Engine) SetFreguesias(r *admin.FreguesiaResolver) {
	e.freguesias = r
}

// loadedLayer pairs a layer with its (attempted) load result.
type loadedLayer struct {
	layer  adapter.Layer
	loaded source.Loaded
	err    error
}

// prefetch loads every layer concurrently (live layers each pay their own
// network latency, so sequential loading multiplies it), preserving the
// adapter's layer order for deterministic results.
func prefetch(ctx context.Context, layers []adapter.Layer) []loadedLayer {
	out := make([]loadedLayer, len(layers))
	var wg sync.WaitGroup
	for i, l := range layers {
		wg.Add(1)
		go func(i int, l adapter.Layer) {
			defer wg.Done()
			loaded, err := l.Loader(ctx)
			out[i] = loadedLayer{layer: l, loaded: loaded, err: err}
		}(i, l)
	}
	wg.Wait()
	return out
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
		res.Notes = append(res.Notes, undeterminedNote(lon, lat))
		return res, nil
	}
	res.Municipality = muni.Name
	if e.freguesias != nil {
		res.Freguesia, _ = e.freguesias.ResolvePoint(lon, lat)
	}

	ad, dedicated := registry.Resolve(muni.Name, muni.Code)
	res.Supported = true
	if !dedicated {
		res.Notes = append(res.Notes, zoningOnlyNote(muni.Name))
	}
	plan := ad.Plan()
	res.Plan = &plan

	opts := e.opts
	bb := pointBBox(lon, lat, 0.005)
	opts.BBox = &bb

	pt := spatial.Point(lon, lat)
	collector := newSourceSet()
	confidence := ad.BaseConfidence()
	zoneSeen := map[string]bool{}

	for _, ll := range prefetch(ctx, ad.Layers(opts)) {
		layer, loaded := ll.layer, ll.loaded
		if ll.err != nil {
			res.Notes = append(res.Notes, fmt.Sprintf("Layer %q unavailable: %v", layer.Title, ll.err))
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
	res.Regulation = attachRegulation(ad, res.Zoning)
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
		cx, cy := centroid(g)
		res.Notes = append(res.Notes, undeterminedNote(cx, cy))
		return res, nil
	}
	res.Municipality = best.Municipality.Name
	if e.freguesias != nil {
		res.Freguesia, _ = e.freguesias.ResolvePolygon(g)
	}
	// Analyse only the portion inside the resolved municipality, so zoning and
	// constraint percentages are shares of the analysed area — not of a parcel
	// that may straddle a boundary into unanalysed municipalities.
	total := best.AreaM2
	res.AnalysedAreaM2 = total
	if len(overlaps) > 1 {
		res.Notes = append(res.Notes, straddleNote(best, overlaps))
	}

	ad, dedicated := registry.Resolve(best.Municipality.Name, best.Municipality.Code)
	res.Supported = true
	if !dedicated {
		res.Notes = append(res.Notes, zoningOnlyNote(best.Municipality.Name))
	}
	plan := ad.Plan()
	res.Plan = &plan

	opts := e.opts
	bb := geomBBox(g, 0.005)
	opts.BBox = &bb

	collector := newSourceSet()
	confidence := ad.BaseConfidence()

	for _, ll := range prefetch(ctx, ad.Layers(opts)) {
		layer, loaded := ll.layer, ll.loaded
		if ll.err != nil {
			res.Notes = append(res.Notes, fmt.Sprintf("Layer %q unavailable: %v", layer.Title, ll.err))
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

	res.Regulation = attachRegulation(ad, res.Zoning)
	res.Sources = collector.list()
	res.Confidence = confidence
	return res, nil
}

// PointMap builds the map data for a coordinate query: the municipality
// boundary, the zoning polygon at the point, and nearby constraint zones,
// clipped to a local view. Returns nil if the location is unsupported.
func (e *Engine) PointMap(ctx context.Context, lon, lat float64) *mapview.Data {
	muni, ok := e.resolver.ResolvePoint(lon, lat)
	if !ok {
		return nil
	}
	ad, _ := registry.Resolve(muni.Name, muni.Code)
	view := viewAround(lon, lat, 3000)
	d := &mapview.Data{Subject: spatial.Point(lon, lat), SubjectKind: "point"}
	d.MinLon, d.MinLat, d.MaxLon, d.MaxLat = view.minLon, view.minLat, view.maxLon, view.maxLat

	if b, ok := e.resolver.BoundaryAt(lon, lat); ok {
		if bc := clipTo(b, view.rect); !bc.IsEmpty() {
			d.Layers = append(d.Layers, mapview.Layer{ID: "boundary", Label: "Município de " + muni.Name, Role: mapview.RoleBoundary, G: bc})
		}
	}

	opts := e.opts
	bb := view.bbox()
	opts.BBox = &bb
	pt := spatial.Point(lon, lat)

	for _, ll := range prefetch(ctx, ad.Layers(opts)) {
		layer, loaded := ll.layer, ll.loaded
		if ll.err != nil {
			continue
		}
		switch layer.Kind {
		case adapter.KindZoning:
			var at []spatial.Feature
			label := "Zonamento"
			for _, f := range loaded.Features {
				if spatial.Intersects(pt, f.Geometry) {
					at = append(at, f)
					label = classify(layer, f).Label
				}
			}
			if g, _ := spatial.Coverage(view.rect, at); !g.IsEmpty() {
				d.Layers = append(d.Layers, mapview.Layer{ID: "zoning", Label: label, Role: mapview.RoleZoning, G: simplify(g)})
			}
		case adapter.KindConstraint:
			present := false
			var near []spatial.Feature
			for _, f := range loaded.Features {
				if spatial.Intersects(pt, f.Geometry) {
					present = true
				}
				if spatial.Intersects(view.rect, f.Geometry) {
					near = append(near, f)
				}
			}
			if len(near) == 0 {
				continue
			}
			if g, _ := spatial.Coverage(view.rect, near); !g.IsEmpty() {
				role := mapview.RoleAbsent
				if present {
					role = mapview.RolePresent
				}
				d.Layers = append(d.Layers, mapview.Layer{ID: layer.ID, Label: layer.Constraint, Role: role, G: simplify(g)})
			}
		}
	}
	return d
}

// PolygonMap builds the map data for a parcel query.
func (e *Engine) PolygonMap(ctx context.Context, g geom.Geometry) *mapview.Data {
	best, _, ok := e.resolver.ResolvePolygon(g)
	if !ok {
		return nil
	}
	ad, _ := registry.Resolve(best.Municipality.Name, best.Municipality.Code)
	min, max, _ := g.Envelope().MinMaxXYs()
	cx, cy := (min.X+max.X)/2, (min.Y+max.Y)/2
	// view spans the parcel plus a margin
	spanM := math.Max(distM(min.X, cy, max.X, cy), distM(cx, min.Y, cx, max.Y))
	view := viewAround(cx, cy, math.Max(spanM*0.9, 800))
	d := &mapview.Data{Subject: g, SubjectKind: "polygon"}
	d.MinLon, d.MinLat, d.MaxLon, d.MaxLat = view.minLon, view.minLat, view.maxLon, view.maxLat

	if b, ok := e.resolver.BoundaryAt(cx, cy); ok {
		if bc := clipTo(b, view.rect); !bc.IsEmpty() {
			d.Layers = append(d.Layers, mapview.Layer{ID: "boundary", Label: "Município de " + best.Municipality.Name, Role: mapview.RoleBoundary, G: bc})
		}
	}
	opts := e.opts
	bb := view.bbox()
	opts.BBox = &bb
	for _, ll := range prefetch(ctx, ad.Layers(opts)) {
		layer, loaded := ll.layer, ll.loaded
		if ll.err != nil {
			continue
		}
		var near []spatial.Feature
		present := false
		for _, f := range loaded.Features {
			if spatial.Intersects(view.rect, f.Geometry) {
				near = append(near, f)
			}
			if layer.Kind == adapter.KindConstraint && spatial.Intersects(g, f.Geometry) {
				present = true
			}
		}
		if len(near) == 0 {
			continue
		}
		cov, _ := spatial.Coverage(view.rect, near)
		if cov.IsEmpty() {
			continue
		}
		if layer.Kind == adapter.KindZoning {
			d.Layers = append(d.Layers, mapview.Layer{ID: "zoning", Label: "Zonamento", Role: mapview.RoleZoning, G: simplify(cov)})
		} else {
			role := mapview.RoleAbsent
			if present {
				role = mapview.RolePresent
			}
			d.Layers = append(d.Layers, mapview.Layer{ID: layer.ID, Label: layer.Constraint, Role: role, G: simplify(cov)})
		}
	}
	return d
}

type viewBox struct {
	minLon, minLat, maxLon, maxLat float64
	rect                           geom.Geometry
}

func (v viewBox) bbox() source.BBox {
	return source.BBox{MinLon: v.minLon, MinLat: v.minLat, MaxLon: v.maxLon, MaxLat: v.maxLat}
}

// viewAround returns a view box of roughly halfM metres half-width centred on
// (lon,lat).
func viewAround(lon, lat, halfM float64) viewBox {
	kx := math.Cos(lat * math.Pi / 180)
	lonHalf := halfM / (111320 * kx)
	latHalf := halfM / 111320
	minLon, maxLon := lon-lonHalf, lon+lonHalf
	minLat, maxLat := lat-latHalf, lat+latHalf
	gj := fmt.Sprintf(`{"type":"Polygon","coordinates":[[[%g,%g],[%g,%g],[%g,%g],[%g,%g],[%g,%g]]]}`,
		minLon, minLat, maxLon, minLat, maxLon, maxLat, minLon, maxLat, minLon, minLat)
	rect, _ := geom.UnmarshalGeoJSON([]byte(gj), geom.NoValidate{})
	return viewBox{minLon, minLat, maxLon, maxLat, rect}
}

func clipTo(g, rect geom.Geometry) geom.Geometry {
	c, err := spatial.Intersection(g, rect)
	if err != nil {
		return geom.Geometry{}
	}
	return simplify(c)
}

func simplify(g geom.Geometry) geom.Geometry {
	if s, err := g.Simplify(0.00003, geom.NoValidate{}); err == nil && !s.IsEmpty() {
		return s
	}
	return g
}

func distM(lon1, lat1, lon2, lat2 float64) float64 {
	kx := math.Cos((lat1 + lat2) / 2 * math.Pi / 180)
	return math.Hypot((lon2-lon1)*111320*kx, (lat2-lat1)*111320)
}

// attachRegulation retrieves the regulation articles applicable to the result's
// zoning categories. This is pure retrieval: it hands over the rules to read,
// it does not interpret them into building parameters.
func attachRegulation(ad adapter.Adapter, zoning []model.ZoningHit) *model.Regulation {
	store := ad.Regulation()
	if store == nil {
		return nil
	}
	arts := store.MatchSection(zoningTerms(zoning)...)
	return &model.Regulation{
		Reference: store.Reference,
		URL:       store.URL,
		Articles:  arts,
		Note: "Candidate articles retrieved by matching the zoning category to regulation sections. " +
			"Read them in full to determine the building rules — this tool does not interpret them, and it is not legal advice.",
	}
}

func zoningTerms(zoning []model.ZoningHit) []string {
	seen := map[string]bool{}
	var terms []string
	for _, z := range zoning {
		for _, t := range []string{z.Subclass, z.Label} {
			if t != "" && !seen[t] {
				seen[t] = true
				terms = append(terms, t)
			}
		}
	}
	return terms
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

// zoningOnlyNote explains what a generic (no dedicated adapter) result does and
// does not cover. It must be honest: the missing constraint layers are exactly
// what a per-municipality adapter adds.
func zoningOnlyNote(muni string) string {
	return fmt.Sprintf(
		"Zoning for %s comes from the national DGT CRUS dataset, and the national servidões "+
			"(Rede Natura 2000, perigosidade de incêndio rural) from the DGT SRUP datasets (fetched live and cached). "+
			"Municipal constraint layers (RAN, REN, servidões locais) and the written regulation (Regulamento) "+
			"are not yet integrated for this municipality — do not read the absence of a constraint as its inexistence.",
		muni)
}

// undeterminedNote explains why no municipality matched, calling out the
// autonomous regions (not covered by the mainland CAOP/CRUS datasets).
func undeterminedNote(lon, lat float64) string {
	switch {
	case lon >= -31.5 && lon <= -24.5 && lat >= 36.5 && lat <= 40.0:
		return "The location is in the Autonomous Region of the Azores, which the mainland DGT datasets (CAOP/CRUS) " +
			"do not cover; the regional planning services are not yet integrated."
	case lon >= -17.5 && lon <= -15.5 && lat >= 29.9 && lat <= 33.5:
		return "The location is in the Autonomous Region of Madeira, which the mainland DGT datasets (CAOP/CRUS) " +
			"do not cover; the regional planning services are not yet integrated."
	default:
		return "The location is outside the bundled administrative coverage (mainland Portugal, CAOP); " +
			"the municipality could not be determined."
	}
}

// centroid returns the centre of a geometry's envelope (fallback 0,0).
func centroid(g geom.Geometry) (lon, lat float64) {
	min, max, ok := g.Envelope().MinMaxXYs()
	if !ok {
		return 0, 0
	}
	return (min.X + max.X) / 2, (min.Y + max.Y) / 2
}

func straddleNote(best admin.Overlap, overlaps []admin.Overlap) string {
	return fmt.Sprintf(
		"Polygon straddles %d municipalities; analysing the majority one (%s, %.1f%% of area). "+
			"Results for the remainder are not included.",
		len(overlaps), best.Municipality.Name, best.Percent)
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
