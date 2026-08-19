// Package query is the spatial query engine: it resolves a location to a
// municipality, loads that municipality's planning layers via its adapter, and
// intersects the input against them to produce a structured result.
package query

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bernardosimoes/pdm/internal/adapter"
	"github.com/bernardosimoes/pdm/internal/adapter/srup"
	"github.com/bernardosimoes/pdm/internal/admin"
	"github.com/bernardosimoes/pdm/internal/crs"
	"github.com/bernardosimoes/pdm/internal/mapview"
	"github.com/bernardosimoes/pdm/internal/model"
	"github.com/bernardosimoes/pdm/internal/national"
	"github.com/bernardosimoes/pdm/internal/planos"
	"github.com/bernardosimoes/pdm/internal/registry"
	"github.com/bernardosimoes/pdm/internal/regstore"
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

// Point answers a coordinate query.
func (e *Engine) Point(ctx context.Context, lon, lat float64) (*model.PointResult, error) {
	return e.PointStream(ctx, lon, lat, nil)
}

// PointStream answers a coordinate query, emitting streaming events as work
// completes when emit is non-nil. The returned result is identical to Point's.
func (e *Engine) PointStream(ctx context.Context, lon, lat float64, emit Emit) (*model.PointResult, error) {
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
		if emit != nil {
			emit(MetaEvent{Event: "meta", Kind: "point", Input: &res.Input, Municipality: res.Municipality})
		}
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

	layers := composeLayers(ad, muni.Name, muni.Code, opts)
	var onDone func(int, evaled)
	if emit != nil {
		var mapOnce sync.Once
		emit(MetaEvent{Event: "meta", Kind: "point", Input: &res.Input,
			Municipality: res.Municipality, Code: muni.Code, Supported: true, Plan: res.Plan, Layers: layerMetas(layers)})
		onDone = func(i int, ev evaled) {
			emit(pointLayerEvent(i, pt, ev))
			// The zoning evaluation already carries the full feature geometry.
			// Reuse it for the locator map instead of issuing a second live CRUS
			// request through PointMap. The municipality boundary is bundled.
			if ev.layer.Kind == adapter.KindZoning {
				mapOnce.Do(func() {
					features := ev.loaded.Features
					if ev.err != nil || ev.probed != nil {
						features = nil
					}
					if m := e.pointMapFromZoning(lon, lat, ev.layer, features); m != nil {
						if svg := m.SVG(); svg != "" {
							emit(MapEvent{Event: "map", HTML: svg})
						}
					}
				})
			}
		}
	}
	for _, ev := range evalLayers(ctx, layers, pointProbeBBox(lon, lat), onDone) {
		layer := ev.layer
		if ev.err != nil {
			res.Notes = append(res.Notes, fmt.Sprintf("Layer %q unavailable: %v", layer.Title, ev.err))
			confidence = downgrade(confidence)
			continue
		}
		if ev.probed != nil {
			collector.add(ev.probed.Source)
			res.Constraints = append(res.Constraints, probedHit(layer, *ev.probed, ""))
			if ev.probed.Unknown {
				confidence = downgrade(confidence)
			}
			continue
		}
		loaded := ev.loaded
		collector.add(loaded.Source)
		if isSample(loaded.Source) {
			confidence = model.ConfidenceLow
		}

		zoning, constraint := pointLayerHits(pt, ev)
		switch layer.Kind {
		case adapter.KindZoning:
			for _, z := range zoning {
				if zoneSeen[z.Label] { // overlapping layers can repeat a class
					continue
				}
				zoneSeen[z.Label] = true
				res.Zoning = append(res.Zoning, z)
			}
		case adapter.KindConstraint:
			if constraint != nil {
				res.Constraints = append(res.Constraints, *constraint)
			}
		}
	}

	if len(res.Zoning) == 0 {
		res.Notes = append(res.Notes, "No zoning category matched the coordinate in the available layers.")
		confidence = downgrade(confidence)
	}
	res.Instruments = enrichInstruments(planos.ForMunicipality(muni.Name), res.Constraints)
	res.Regulation = attachRegulation(ad, muni.Code, res.Zoning)
	res.Sources = collector.list()
	res.Confidence = confidence
	return res, nil
}

// Polygon answers a polygon (parcel) query.
func (e *Engine) Polygon(ctx context.Context, g geom.Geometry) (*model.PolygonResult, error) {
	return e.PolygonStream(ctx, g, nil)
}

// PolygonStream answers a polygon query, emitting streaming events as work
// completes when emit is non-nil. The returned result is identical to Polygon's.
func (e *Engine) PolygonStream(ctx context.Context, g geom.Geometry, emit Emit) (*model.PolygonResult, error) {
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
		if emit != nil {
			emit(MetaEvent{Event: "meta", Kind: "polygon", AnalysedAreaM2: res.AnalysedAreaM2,
				Municipality: res.Municipality})
		}
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

	layers := composeLayers(ad, best.Municipality.Name, best.Municipality.Code, opts)
	var onDone func(int, evaled)
	if emit != nil {
		emit(MetaEvent{Event: "meta", Kind: "polygon", AnalysedAreaM2: total,
			Municipality: res.Municipality, Code: best.Municipality.Code, Supported: true, Plan: res.Plan, Layers: layerMetas(layers)})
		onDone = func(i int, ev evaled) {
			emit(polygonLayerEvent(i, g, total, ev))
		}
	}
	for _, ev := range evalLayers(ctx, layers, geomBBox(g, 0), onDone) {
		layer := ev.layer
		if ev.err != nil {
			res.Notes = append(res.Notes, fmt.Sprintf("Layer %q unavailable: %v", layer.Title, ev.err))
			confidence = downgrade(confidence)
			continue
		}
		if ev.probed != nil {
			collector.add(ev.probed.Source)
			res.Constraints = append(res.Constraints, probedHit(layer, *ev.probed, envelopeNote))
			if ev.probed.Unknown {
				confidence = downgrade(confidence)
			}
			continue
		}
		loaded := ev.loaded
		collector.add(loaded.Source)
		if isSample(loaded.Source) {
			confidence = model.ConfidenceLow
		}

		zoning, constraint := polygonLayerHits(g, total, ev)
		switch layer.Kind {
		case adapter.KindZoning:
			res.Zoning = append(res.Zoning, zoning...)
		case adapter.KindConstraint:
			if constraint != nil {
				res.Constraints = append(res.Constraints, *constraint)
			}
		}
	}

	if len(res.Zoning) == 0 {
		res.Notes = append(res.Notes, "No zoning category matched the parcel in the available layers.")
		confidence = downgrade(confidence)
	}

	sort.SliceStable(res.Zoning, func(i, j int) bool { return res.Zoning[i].AreaM2 > res.Zoning[j].AreaM2 })
	sort.SliceStable(res.Constraints, func(i, j int) bool { return res.Constraints[i].AreaM2 > res.Constraints[j].AreaM2 })

	res.Instruments = enrichInstruments(planos.ForMunicipality(best.Municipality.Name), res.Constraints)
	res.Regulation = attachRegulation(ad, best.Municipality.Code, res.Zoning)
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

	for _, ll := range prefetchMapLayers(ctx, geometryLayers(ad, opts)) {
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

// pointMapFromZoning builds the streamed point locator map from zoning
// features that were already loaded by PointStream. It deliberately draws only
// the municipality boundary, the queried point and its zoning polygon: nearby
// context remains available on the interactive Leaflet map, and no second
// network request is needed here.
func (e *Engine) pointMapFromZoning(lon, lat float64, layer adapter.Layer, features []spatial.Feature) *mapview.Data {
	muni, ok := e.resolver.ResolvePoint(lon, lat)
	if !ok {
		return nil
	}
	view := viewAround(lon, lat, 3000)
	d := &mapview.Data{Subject: spatial.Point(lon, lat), SubjectKind: "point"}
	d.MinLon, d.MinLat, d.MaxLon, d.MaxLat = view.minLon, view.minLat, view.maxLon, view.maxLat

	if b, ok := e.resolver.BoundaryAt(lon, lat); ok {
		if bc := clipTo(b, view.rect); !bc.IsEmpty() {
			d.Layers = append(d.Layers, mapview.Layer{ID: "boundary", Label: "Município de " + muni.Name, Role: mapview.RoleBoundary, G: bc})
		}
	}

	pt := spatial.Point(lon, lat)
	var at []spatial.Feature
	label := "Zonamento"
	for _, f := range features {
		if spatial.Intersects(pt, f.Geometry) {
			at = append(at, f)
			label = classify(layer, f).Label
		}
	}
	if g, _ := spatial.Coverage(view.rect, at); !g.IsEmpty() {
		d.Layers = append(d.Layers, mapview.Layer{ID: "zoning", Label: label, Role: mapview.RoleZoning, G: simplify(g)})
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
	total := crs.AreaM2(g)
	for _, ll := range prefetchMapLayers(ctx, geometryLayers(ad, opts)) {
		layer, loaded := ll.layer, ll.loaded
		if ll.err != nil {
			continue
		}
		if layer.Kind == adapter.KindZoning {
			d.Layers = append(d.Layers, zoningMapLayers(g, total, layer, loaded.Features)...)
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
		role := mapview.RoleAbsent
		if present {
			role = mapview.RolePresent
		}
		d.Layers = append(d.Layers, mapview.Layer{ID: layer.ID, Label: layer.Constraint, Role: role, G: simplify(cov)})
	}
	return d
}

func zoningMapLayers(g geom.Geometry, total float64, layer adapter.Layer, feats []spatial.Feature) []mapview.Layer {
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
	var out []mapview.Layer
	for i, key := range order {
		grp := groups[key]
		cov, err := spatial.Coverage(g, grp.feats)
		if err != nil || cov.IsEmpty() {
			continue
		}
		label := grp.c.Subclass
		if label == "" {
			label = grp.c.Label
		}
		if area := crs.AreaM2(cov); total > 0 && area > 0 {
			label = fmt.Sprintf("%s %.1f%%", label, percent(area, total))
		}
		id := "zoning"
		if i > 0 {
			id = fmt.Sprintf("zoning-%d", i+1)
		}
		out = append(out, mapview.Layer{
			ID:    id,
			Label: label,
			Role:  mapview.RoleZoning,
			G:     simplify(cov),
			Color: zoningColor(grp.c),
		})
	}
	return out
}

func zoningColor(c adapter.Classification) string {
	s := strings.ToLower(c.Label + " " + c.Subclass + " " + c.Class)
	switch {
	case strings.Contains(s, "habit"):
		return "#4f6fd9"
	case strings.Contains(s, "natural") || strings.Contains(s, "paisag"):
		return "#2f8f6b"
	case strings.Contains(s, "turíst") || strings.Contains(s, "turist"):
		return "#8b5fc7"
	case strings.Contains(s, "florest"):
		return "#4f8a3d"
	case strings.Contains(s, "agr"):
		return "#c9912e"
	case strings.Contains(s, "equip") || strings.Contains(s, "infra"):
		return "#2f8fa3"
	case strings.Contains(s, "urb") || strings.Contains(s, "edific"):
		return "#d46a3a"
	default:
		return "#7a6f8f"
	}
}

// composeLayers merges the adapter's own layers with the national constraint
// layers available for every mainland municipality. National layers whose
// canonical constraint type the adapter already provides (e.g. Tomar's bundled
// RAN/REN) are skipped — the dedicated source is richer.
func composeLayers(ad adapter.Adapter, name, code string, opts source.Options) []adapter.Layer {
	layers := geometryLayers(ad, opts)
	skip := map[string]bool{}
	for _, l := range layers {
		if l.Constraint != "" {
			skip[l.Constraint] = true
		}
	}
	return append(layers, national.Layers(name, code, opts, skip)...)
}

// geometryLayers is the drawable layer set: the adapter's own layers plus the
// national SRUP geometry layers (Rede Natura 2000 ZPE/ZEC, rural fire hazard),
// deduped by constraint string. The geometry layers come before the national
// probes in composeLayers: geometry yields real overlap area and percentages,
// so where both exist the geometry layer wins and the equivalent probe is
// skipped. The map builders use this same set, so every geometry constraint
// the report lists is also drawable on the locator map.
func geometryLayers(ad adapter.Adapter, opts source.Options) []adapter.Layer {
	layers := ad.Layers(opts)
	skip := map[string]bool{}
	for _, l := range layers {
		if l.Constraint != "" {
			skip[l.Constraint] = true
		}
	}
	for _, l := range srup.Layers(opts) {
		if skip[l.Constraint] {
			continue
		}
		skip[l.Constraint] = true
		layers = append(layers, l)
	}
	return layers
}

// loadedMapLayer pairs a drawable layer with its (attempted) load result.
type loadedMapLayer struct {
	layer  adapter.Layer
	loaded source.Loaded
	err    error
}

// prefetchMapLayers loads the drawable (non-probe) layers concurrently — each
// live layer pays its own network latency, so sequential loading multiplies
// it — preserving layer order for deterministic map output.
func prefetchMapLayers(ctx context.Context, layers []adapter.Layer) []loadedMapLayer {
	drawable := layers[:0:0]
	for _, l := range layers {
		if l.Probe == nil {
			drawable = append(drawable, l)
		}
	}
	out := make([]loadedMapLayer, len(drawable))
	var wg sync.WaitGroup
	for i, l := range drawable {
		wg.Add(1)
		go func(i int, l adapter.Layer) {
			defer wg.Done()
			loaded, err := l.Loader(ctx)
			out[i] = loadedMapLayer{layer: l, loaded: loaded, err: err}
		}(i, l)
	}
	wg.Wait()
	return out
}

// evaled is one layer's outcome: features for geometry layers, a probe result
// for probe layers, or an error.
type evaled struct {
	layer  adapter.Layer
	loaded source.Loaded
	probed *source.Probed
	err    error
}

// evalLayers evaluates all layers concurrently (bounded), preserving order.
// Layers are independent HTTP fetches against slow public services, so
// parallelism cuts wall-clock roughly by the number of layers. onDone, when
// non-nil, is invoked from the worker goroutine as each layer finishes.
func evalLayers(ctx context.Context, layers []adapter.Layer, probeBB source.BBox, onDone func(i int, ev evaled)) []evaled {
	out := make([]evaled, len(layers))
	sem := make(chan struct{}, 10)
	var wg sync.WaitGroup
	for i, l := range layers {
		wg.Add(1)
		go func(i int, l adapter.Layer) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if l.Probe != nil {
				p, err := l.Probe(ctx, probeBB)
				out[i] = evaled{layer: l, probed: &p, err: err}
			} else {
				loaded, err := l.Loader(ctx)
				out[i] = evaled{layer: l, loaded: loaded, err: err}
			}
			if onDone != nil {
				onDone(i, out[i])
			}
		}(i, l)
	}
	wg.Wait()
	return out
}

// envelopeNote is the caveat attached to probed constraints in polygon queries,
// where presence is tested against the polygon's envelope.
const envelopeNote = "Presença avaliada pelo envelope do polígono (aproximação); área e percentagem não calculadas."

// pointLayerHits extracts one loaded layer's hits at a point. Zoning labels are
// deduped within the layer only; callers apply cross-layer dedup.
func pointLayerHits(pt geom.Geometry, ev evaled) (zoning []model.ZoningHit, constraint *model.ConstraintHit) {
	layer := ev.layer
	switch layer.Kind {
	case adapter.KindZoning:
		seen := map[string]bool{}
		for _, f := range ev.loaded.Features {
			if !spatial.Intersects(pt, f.Geometry) {
				continue
			}
			c := classify(layer, f)
			if seen[c.Label] { // overlapping polygons can repeat a class
				continue
			}
			seen[c.Label] = true
			zoning = append(zoning, model.ZoningHit{
				Class:    c.Class,
				Subclass: c.Subclass,
				Label:    c.Label,
				RawCode:  c.RawCode,
				Layer:    layer.ID,
				Color:    zoningColor(c),
			})
		}
	case adapter.KindConstraint:
		hit := model.ConstraintHit{
			Type:  layer.Constraint,
			Label: layer.Title,
			Layer: layer.ID,
		}
		for _, f := range ev.loaded.Features {
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
		constraint = &hit
	}
	return zoning, constraint
}

// polygonLayerHits extracts one loaded layer's hits across a parcel.
func polygonLayerHits(g geom.Geometry, total float64, ev evaled) (zoning []model.ZoningHit, constraint *model.ConstraintHit) {
	layer := ev.layer
	switch layer.Kind {
	case adapter.KindZoning:
		zoning = zoningBreakdown(g, total, layer, ev.loaded.Features)
	case adapter.KindConstraint:
		hit := constraintCoverage(g, total, layer, ev.loaded.Features)
		constraint = &hit
	}
	return zoning, constraint
}

// pointLayerEvent converts an evaluated layer into its streaming event.
func pointLayerEvent(i int, pt geom.Geometry, ev evaled) LayerEvent {
	le := LayerEvent{Event: "layer", Index: i, ID: ev.layer.ID}
	switch {
	case ev.err != nil:
		le.Error = fmt.Sprintf("unavailable: %v", ev.err)
	case ev.probed != nil:
		h := probedHit(ev.layer, *ev.probed, "")
		le.Constraint = &h
	default:
		le.Zoning, le.Constraint = pointLayerHits(pt, ev)
		if ev.layer.Kind == adapter.KindZoning {
			le.ZoningGeoJSON = pointZoningGeoJSON(pt, ev.layer, ev.loaded.Features)
		}
	}
	return le
}

// polygonLayerEvent converts an evaluated layer into its streaming event.
func polygonLayerEvent(i int, g geom.Geometry, total float64, ev evaled) LayerEvent {
	le := LayerEvent{Event: "layer", Index: i, ID: ev.layer.ID}
	switch {
	case ev.err != nil:
		le.Error = fmt.Sprintf("unavailable: %v", ev.err)
	case ev.probed != nil:
		h := probedHit(ev.layer, *ev.probed, envelopeNote)
		le.Constraint = &h
	default:
		le.Zoning, le.Constraint = polygonLayerHits(g, total, ev)
		if ev.layer.Kind == adapter.KindZoning {
			le.ZoningGeoJSON = zoningGeoJSON(g, total, ev.layer, ev.loaded.Features)
		}
	}
	return le
}

type zoningFeatureCollection struct {
	Type     string          `json:"type"`
	Features []zoningFeature `json:"features"`
}

type zoningFeature struct {
	Type       string          `json:"type"`
	Properties map[string]any  `json:"properties"`
	Geometry   json.RawMessage `json:"geometry"`
}

// pointZoningGeoJSON returns the complete zoning polygons that contain a
// queried point. Unlike zoningGeoJSON for parcels, it must not intersect the
// geometry with the zero-area point: doing so would collapse every polygon and
// leave the interactive map with nothing to draw.
func pointZoningGeoJSON(pt geom.Geometry, layer adapter.Layer, feats []spatial.Feature) *zoningFeatureCollection {
	type group struct {
		c     adapter.Classification
		geoms []geom.Geometry
	}
	order := []string{}
	groups := map[string]*group{}
	for _, f := range feats {
		if !spatial.Intersects(pt, f.Geometry) {
			continue
		}
		c := classify(layer, f)
		if groups[c.Label] == nil {
			groups[c.Label] = &group{c: c}
			order = append(order, c.Label)
		}
		groups[c.Label].geoms = append(groups[c.Label].geoms, f.Geometry)
	}
	fc := &zoningFeatureCollection{Type: "FeatureCollection"}
	for _, key := range order {
		grp := groups[key]
		g, err := spatial.UnionAll(grp.geoms)
		if err != nil || g.IsEmpty() {
			continue
		}
		raw, err := simplify(g).MarshalJSON()
		if err != nil {
			continue
		}
		fc.Features = append(fc.Features, zoningFeature{
			Type: "Feature",
			Properties: map[string]any{
				"class":    grp.c.Class,
				"subclass": grp.c.Subclass,
				"label":    grp.c.Label,
				"color":    zoningColor(grp.c),
			},
			Geometry: json.RawMessage(raw),
		})
	}
	if len(fc.Features) == 0 {
		return nil
	}
	return fc
}

func zoningGeoJSON(g geom.Geometry, total float64, layer adapter.Layer, feats []spatial.Feature) *zoningFeatureCollection {
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
	fc := &zoningFeatureCollection{Type: "FeatureCollection"}
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
		raw, err := simplify(cov).MarshalJSON()
		if err != nil {
			continue
		}
		fc.Features = append(fc.Features, zoningFeature{
			Type: "Feature",
			Properties: map[string]any{
				"class":    grp.c.Class,
				"subclass": grp.c.Subclass,
				"label":    grp.c.Label,
				"area_m2":  area,
				"percent":  percent(area, total),
				"color":    zoningColor(grp.c),
			},
			Geometry: json.RawMessage(raw),
		})
	}
	if len(fc.Features) == 0 {
		return nil
	}
	return fc
}

// probedHit converts a probe outcome into a constraint hit; extraNote is
// appended for callers that add their own caveat (polygon envelope tests).
func probedHit(layer adapter.Layer, p source.Probed, extraNote string) model.ConstraintHit {
	note := p.Note
	if extraNote != "" {
		if note != "" {
			note += " "
		}
		note += extraNote
	}
	return model.ConstraintHit{
		Type:    layer.Constraint,
		Label:   layer.Title,
		Layer:   layer.ID,
		Present: p.Present,
		Unknown: p.Unknown,
		Detail:  p.Detail,
		Note:    note,
	}
}

// pointProbeBBox is the tight envelope used for server-side presence probes at
// a coordinate: ±0.00002° (about two metres).
func pointProbeBBox(lon, lat float64) source.BBox {
	const eps = 0.00002
	return source.BBox{MinLon: lon - eps, MinLat: lat - eps, MaxLon: lon + eps, MaxLat: lat + eps}
}

// enrichInstruments attaches the registry's special instruments for the
// municipality, marking PointInside=true where a live layer positively named
// the instrument's subject at the location. Absence of a match leaves nil —
// most instruments' full areas (e.g. a POAAP's 500 m belt) are wider than the
// layers checked, so "not confirmed inside" must never read as "outside".
func enrichInstruments(instruments []model.Instrument, hits []model.ConstraintHit) []model.Instrument {
	if len(instruments) == 0 {
		return nil
	}
	var details []string
	for _, h := range hits {
		if !h.Present {
			continue
		}
		details = append(details, h.Type+" "+h.Label+" "+h.Detail)
	}
	for i := range instruments {
		ins := &instruments[i]
		for _, d := range details {
			if planos.Match(ins.ID, d) {
				v := true
				ins.PointInside = &v
				break
			}
		}
	}
	return instruments
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
// it does not interpret them into building parameters. The adapter's own parsed
// regulation wins; otherwise the data-driven regstore (keyed by the resolved
// municipality's dtcc code) supplies it, so any bundled municipality gets
// regulation with no per-municipality adapter.
func attachRegulation(ad adapter.Adapter, code string, zoning []model.ZoningHit) *model.Regulation {
	store := ad.Regulation()
	if store == nil {
		store = regstore.ForCode(code)
	}
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
			Color:    zoningColor(grp.c),
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
// does not cover. Zoning and the national constraint layers are checked for
// every mainland municipality; what a dedicated adapter still adds is the
// municipality's own condicionantes detail and the parsed written regulation.
func zoningOnlyNote(muni string) string {
	return fmt.Sprintf(
		"%s has no dedicated adapter yet: zoning comes from the national DGT CRUS dataset and the "+
			"constraints above from national services (DGT/SNIT SRUP, APA/SNIAmb), fetched live and cached. "+
			"Municipality-specific condicionantes layers and the written regulation (Regulamento) are not "+
			"integrated — do not read missing local detail as inexistence.",
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
