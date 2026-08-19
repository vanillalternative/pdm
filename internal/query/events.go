package query

import (
	"github.com/bernardosimoes/pdm/internal/adapter"
	"github.com/bernardosimoes/pdm/internal/model"
)

// Emit delivers one streaming event. It must be safe for concurrent use —
// layer events are emitted from evaluation worker goroutines. A nil Emit
// disables streaming.
type Emit func(v any)

// The streaming protocol is newline-delimited JSON, one event per line:
// exactly one MetaEvent first, then LayerEvents in completion order, an
// optional MapEvent, and exactly one terminal event (ResultEvent or
// ErrorEvent) last.

// LayerMeta announces one composed layer up-front so a client can render a
// placeholder row before the layer's evaluation completes.
type LayerMeta struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Kind       string `json:"kind"` // "zoning" | "constraint"
	Constraint string `json:"constraint,omitempty"`
}

// MetaEvent is the first event: the location is resolved and the work list is
// known. Supported=false means no layer events will follow.
type MetaEvent struct {
	Event          string            `json:"event"` // "meta"
	Kind           string            `json:"kind"`  // "point" | "polygon"
	Input          *model.Coordinate `json:"input,omitempty"`
	AnalysedAreaM2 float64           `json:"analysed_area_m2,omitempty"`
	Municipality   string            `json:"municipality"`
	Code           string            `json:"code,omitempty"` // CAOP dtmn/dtcc code, for demand tracking
	Supported      bool              `json:"supported"`
	Plan           *model.PlanInfo   `json:"plan,omitempty"`
	Layers         []LayerMeta       `json:"layers,omitempty"`
}

// LayerEvent reports one layer's outcome as soon as it is evaluated. Index
// refers back to MetaEvent.Layers (IDs alone need not be unique).
type LayerEvent struct {
	Event         string               `json:"event"` // "layer"
	Index         int                  `json:"i"`
	ID            string               `json:"id"`
	Zoning        []model.ZoningHit    `json:"zoning,omitempty"`
	ZoningGeoJSON any                  `json:"zoning_geojson,omitempty"`
	Constraint    *model.ConstraintHit `json:"constraint,omitempty"`
	Error         string               `json:"error,omitempty"`
}

// MapEvent carries the self-contained locator-map figure HTML.
type MapEvent struct {
	Event string `json:"event"` // "map"
	HTML  string `json:"html"`
}

// ResultEvent is the successful terminal event; Data is the complete result,
// identical to the non-streaming JSON output.
type ResultEvent struct {
	Event string `json:"event"` // "result"
	Data  any    `json:"data"`
}

// ErrorEvent is the failing terminal event.
type ErrorEvent struct {
	Event string `json:"event"` // "error"
	Error string `json:"error"`
}

func layerMetas(layers []adapter.Layer) []LayerMeta {
	out := make([]LayerMeta, len(layers))
	for i, l := range layers {
		kind := "zoning"
		if l.Kind == adapter.KindConstraint {
			kind = "constraint"
		}
		out[i] = LayerMeta{ID: l.ID, Title: l.Title, Kind: kind, Constraint: l.Constraint}
	}
	return out
}
