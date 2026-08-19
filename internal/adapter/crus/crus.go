// Package crus provides the shared pieces for querying the DGT CRUS dataset
// (Carta do Regime de Uso do Solo) — the national, harmonised zoning of every
// mainland municipality's plans in force. Adapters use it to build live zoning
// loaders and to interpret CRUS features into classifications.
package crus

import (
	"github.com/bernardosimoes/pdm/internal/adapter"
	"github.com/bernardosimoes/pdm/internal/model"
	"github.com/bernardosimoes/pdm/internal/source"
	"github.com/bernardosimoes/pdm/internal/spatial"
)

// ItemsURL is the OGC API Features items endpoint of the CRUS collection.
const ItemsURL = "https://ogcapi.dgterritorio.gov.pt/collections/crus/items"

// CollectionURL is the CRUS collection landing page (for source attribution).
const CollectionURL = "https://ogcapi.dgterritorio.gov.pt/collections/crus"

// LiveLoader builds a loader for CRUS zoning filtered to one municipality (by
// its CAOP dtcc/dtmn code) and to the query's bounding box. An empty dtcc
// falls back to bbox-only filtering; intersection tests downstream still keep
// results correct, the attribute filter just trims cross-border noise.
func LiveLoader(dtcc, municipality string, opts source.Options) source.Loader {
	params := map[string]string{}
	if dtcc != "" {
		params["dtcc"] = dtcc
	}
	return source.OGC(source.OGCConfig{
		ItemsURL: ItemsURL,
		Params:   params,
		UseBBox:  true,
		Meta: model.Source{
			Name:  "DGT/SNIT — CRUS (ordenamento do solo) — " + municipality,
			Layer: "ordenamento",
		},
	}, opts)
}

// Classify interprets a CRUS zoning feature (classificação e qualificação do
// solo) into a Classification.
func Classify(f spatial.Feature) adapter.Classification {
	class := f.Prop("classe_2021", "classe", "classificacao")
	sub := f.Prop("categoria_2021", "categoria")
	full := f.Prop("classificacao_e_qualificacao")
	code := f.Prop("codigo", "cod")
	label := full
	if label == "" {
		switch {
		case class != "" && sub != "":
			label = class + " — " + sub
		case sub != "":
			label = sub
		case class != "":
			label = class
		default:
			label = "(não classificado)"
		}
	}
	return adapter.Classification{Class: class, Subclass: sub, Label: label, RawCode: code}
}
