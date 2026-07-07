// Package model defines the domain types produced by a planning query:
// zoning hits, constraint hits, sources, and the point/polygon results that
// the report renderers consume.
package model

import "time"

// Disclaimer is attached to every result. It must always be surfaced to the
// user: this tool performs an automated query against public planning data and
// is not an official municipal certificate or legal opinion.
const Disclaimer = "This is an automated query against public planning data (an approximation). " +
	"It is NOT an official municipal certificate, a certidão, or a legal opinion. " +
	"Always confirm with the competent municipality (Câmara Municipal) before making any decision."

// Confidence expresses how much trust to place in a result, driven by the
// provenance and completeness of the underlying layers.
type Confidence string

const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)

// Provenance describes where a layer's data actually came from, so results can
// be trusted (or not) accordingly.
type Provenance string

const (
	// ProvenanceOfficialLive means fetched live from an official geoservice
	// (DGT/SNIT WFS, OGC API Features, municipal geoportal).
	ProvenanceOfficialLive Provenance = "official-live"
	// ProvenanceOfficialCache means served from a local cache of official data.
	ProvenanceOfficialCache Provenance = "official-cache"
	// ProvenanceBundled means shipped with the binary (a snapshot of official
	// data embedded at build time).
	ProvenanceBundled Provenance = "bundled-snapshot"
	// ProvenanceSample means illustrative sample geometry, NOT authoritative.
	// Any result touching a sample layer is downgraded to low confidence and
	// clearly flagged.
	ProvenanceSample Provenance = "sample"
)

// Source is an attribution record for a piece of data used in a result.
type Source struct {
	Name        string     `json:"name"`                   // human label, e.g. "DGT/SNIT — Planta de Ordenamento"
	Layer       string     `json:"layer,omitempty"`        // source layer/feature-type name
	URL         string     `json:"url,omitempty"`          // service or document URL
	Provenance  Provenance `json:"provenance"`             // how the data was obtained
	RetrievedAt *time.Time `json:"retrieved_at,omitempty"` // when the data was fetched/snapshotted
}

// Document is a link to an official regulation or plan document.
type Document struct {
	Title string `json:"title"`
	URL   string `json:"url,omitempty"`
	Note  string `json:"note,omitempty"` // e.g. "Diário da República ref."
}

// PlanInfo identifies the planning instrument (IGT) in force.
type PlanInfo struct {
	Name          string     `json:"name"` // e.g. "PDM de Tomar"
	Kind          string     `json:"kind"` // e.g. "PDM"
	Municipality  string     `json:"municipality"`
	PublishedRef  string     `json:"published_ref,omitempty"`  // Diário da República reference
	PublishedDate string     `json:"published_date,omitempty"` // ISO date if known
	Documents     []Document `json:"documents,omitempty"`
}

// Article is a single article of a plan's written regulation (Regulamento),
// with its section context. Text is the verbatim article body — the tool
// surfaces it for a human/AI to read but never interprets it.
type Article struct {
	Number  string `json:"number"`
	Title   string `json:"title"`
	Section string `json:"section,omitempty"`
	Text    string `json:"text,omitempty"`
}

// Regulation is the set of regulation articles applicable to a result, retrieved
// by matching the zoning category to regulation sections. It is candidate
// material to read, not an interpretation.
type Regulation struct {
	Reference string    `json:"reference,omitempty"`
	URL       string    `json:"url,omitempty"`
	Articles  []Article `json:"articles"`
	Note      string    `json:"note,omitempty"`
}

// ZoningHit is a land-use / zoning classification affecting the query location.
type ZoningHit struct {
	Class    string `json:"class"`              // top-level classification, e.g. "Solo rústico"
	Subclass string `json:"subclass,omitempty"` // category, e.g. "Espaços agrícolas"
	Label    string `json:"label"`              // human-readable combined label
	RawCode  string `json:"raw_code,omitempty"` // raw attribute value from the source layer
	Layer    string `json:"layer"`              // source layer id
	// The following are populated for polygon queries.
	AreaM2  float64 `json:"area_m2,omitempty"`
	Percent float64 `json:"percent,omitempty"`
}

// ConstraintHit is a restriction/servitude affecting the query location
// (REN, RAN, servidões, protected areas, etc.).
type ConstraintHit struct {
	Type    string `json:"type"`    // canonical type, e.g. "REN", "RAN"
	Label   string `json:"label"`   // human-readable label
	Present bool   `json:"present"` // whether it affects the location at all
	Detail  string `json:"detail,omitempty"`
	Layer   string `json:"layer"` // source layer id
	// Populated for polygon queries.
	AreaM2  float64 `json:"area_m2,omitempty"`
	Percent float64 `json:"percent,omitempty"`
}

// PointResult answers "what applies at this coordinate?".
type PointResult struct {
	Input        Coordinate      `json:"input"`
	Municipality string          `json:"municipality"`
	Freguesia    string          `json:"freguesia,omitempty"`
	Supported    bool            `json:"supported"`
	Plan         *PlanInfo       `json:"plan,omitempty"`
	Zoning       []ZoningHit     `json:"zoning"`
	Constraints  []ConstraintHit `json:"constraints"`
	Regulation   *Regulation     `json:"regulation,omitempty"`
	Sources      []Source        `json:"sources"`
	Confidence   Confidence      `json:"confidence"`
	Notes        []string        `json:"notes,omitempty"`
	Disclaimer   string          `json:"disclaimer"`
	GeneratedAt  time.Time       `json:"generated_at"`
}

// PolygonResult answers "what applies across this parcel?".
type PolygonResult struct {
	Municipality   string          `json:"municipality"`
	Freguesia      string          `json:"freguesia,omitempty"`
	Supported      bool            `json:"supported"`
	Plan           *PlanInfo       `json:"plan,omitempty"`
	AnalysedAreaM2 float64         `json:"analysed_area_m2"`
	Zoning         []ZoningHit     `json:"zoning"`      // sorted desc by area
	Constraints    []ConstraintHit `json:"constraints"` // sorted desc by area
	Regulation     *Regulation     `json:"regulation,omitempty"`
	Sources        []Source        `json:"sources"`
	Confidence     Confidence      `json:"confidence"`
	Notes          []string        `json:"notes,omitempty"`
	Disclaimer     string          `json:"disclaimer"`
	GeneratedAt    time.Time       `json:"generated_at"`
}

// Coordinate is a WGS84 lon/lat pair (as entered by the user).
type Coordinate struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}
