// Package adapter defines the per-municipality contract. Every supported
// municipality provides an Adapter that knows the planning instrument in force
// and how to load and interpret its planning layers. Adding a municipality
// means adding an adapter and registering it — no changes to the query engine.
package adapter

import (
	"github.com/bernardosimoes/pdm/internal/model"
	"github.com/bernardosimoes/pdm/internal/reg"
	"github.com/bernardosimoes/pdm/internal/source"
	"github.com/bernardosimoes/pdm/internal/spatial"
)

// Kind distinguishes land-use/zoning layers from constraint layers, which are
// reported differently (a class vs. a present/absent restriction).
type Kind string

const (
	KindZoning     Kind = "zoning"
	KindConstraint Kind = "constraint"
)

// Classification is the interpreted zoning result for a single feature.
type Classification struct {
	Class    string // top-level, e.g. "Solo rústico"
	Subclass string // category, e.g. "Espaços agrícolas"
	Label    string // combined human label
	RawCode  string // raw attribute value from the source layer
}

// Layer is a planning layer to query: metadata, a lazy loader, and functions to
// interpret each feature.
type Layer struct {
	ID    string // stable id, e.g. "ordenamento", "ran", "ren"
	Title string // human title, e.g. "Planta de Ordenamento"
	Kind  Kind

	// Constraint is the canonical constraint type for KindConstraint layers
	// (e.g. "REN", "RAN"). Empty for zoning layers.
	Constraint string

	// Loader lazily produces the layer's features (bundled/cache/live).
	// Unused when Probe is set.
	Loader source.Loader

	// Probe, when set, answers the layer by asking the source service what
	// intersects the subject (server-side, attributes only) instead of loading
	// feature geometries. Used for national layers whose polygons are far too
	// large to download. Probe layers carry no geometry, so map views skip them.
	Probe source.Prober

	// Classify interprets a zoning feature. Required for KindZoning.
	Classify func(spatial.Feature) Classification

	// Detail optionally produces a short description for a constraint feature.
	Detail func(spatial.Feature) string
}

// Adapter serves one municipality.
type Adapter interface {
	// Municipality is the canonical municipality name, e.g. "Tomar".
	Municipality() string

	// Plan describes the planning instrument (IGT) in force.
	Plan() model.PlanInfo

	// Layers returns the planning layers to query, given runtime options
	// (live vs. bundled, cache, spatial filter).
	Layers(opts source.Options) []Layer

	// BaseConfidence is the confidence ceiling this adapter's data supports.
	BaseConfidence() model.Confidence

	// Regulation returns the plan's parsed written regulation (Regulamento) for
	// retrieval of applicable articles, or nil if none is available.
	Regulation() *reg.Store
}
