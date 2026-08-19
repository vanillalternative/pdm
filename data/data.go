// Package data embeds the bundled snapshot data shipped with the binary:
// administrative boundaries used for municipality resolution and the pilot
// planning layers for Tomar. Bundled data lets the tool work offline and
// deterministically; live official sources can override it at runtime.
package data

import "embed"

// Municipalities holds administrative boundaries for municipality resolution.
//
//go:embed municipalities.geojson
var Municipalities []byte

// Freguesias holds freguesia boundaries for labelling results (CAOP, heavily
// simplified — resolution accuracy ~50 m).
//
//go:embed freguesias.geojson
var Freguesias []byte

// Tomar pilot planning layers.
var (
	//go:embed tomar/ordenamento.geojson
	TomarOrdenamento []byte

	//go:embed tomar/ran.geojson
	TomarRAN []byte

	//go:embed tomar/ren.geojson
	TomarREN []byte

	//go:embed tomar/poacb.geojson
	TomarPOACB []byte
)

// Regulamentos holds the bundled PDM written regulations (Regulamentos), one
// data/regulamentos/<dtcc>.json per covered municipality, each parsed into
// articles with section context (retrieval only — the tool does not interpret).
// index.json is the coverage manifest and visits.json the demand-ranking
// snapshot maintained by the fetch-regulamentos skill; both are ignored by the
// regulation loader, which keys stores by the <dtcc> filename.
//
//go:embed regulamentos/*.json
var Regulamentos embed.FS

// Planos holds the national registry of special planning instruments (planos/
// programas especiais — albufeiras, orla costeira, estuários, áreas
// protegidas): every instrument ever approved or in the pipeline, its diploma,
// status and the municipalities it touches. Compiled from the official APA,
// ICNF, DGT/PCGT and DRE registries (verified July 2026).
//
//go:embed planos.json
var Planos []byte
