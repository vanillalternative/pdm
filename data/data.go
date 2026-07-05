// Package data embeds the bundled snapshot data shipped with the binary:
// administrative boundaries used for municipality resolution and the pilot
// planning layers for Tomar. Bundled data lets the tool work offline and
// deterministically; live official sources can override it at runtime.
package data

import _ "embed"

// Municipalities holds administrative boundaries for municipality resolution.
//
//go:embed municipalities.geojson
var Municipalities []byte

// Tomar pilot planning layers.
var (
	//go:embed tomar/ordenamento.geojson
	TomarOrdenamento []byte

	//go:embed tomar/ran.geojson
	TomarRAN []byte

	//go:embed tomar/ren.geojson
	TomarREN []byte
)

// TomarRegulamento holds the PDM de Tomar written regulation, parsed into
// articles with section context (retrieval only — the tool does not interpret).
//
//go:embed tomar/regulamento.json
var TomarRegulamento []byte
