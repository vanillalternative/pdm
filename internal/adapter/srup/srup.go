// Package srup provides the national constraint layers from the DGT SRUP
// datasets (Servidões e Restrições de Utilidade Pública): Rede Natura 2000
// (ZPE and ZEC) and the rural fire hazard chart. Unlike RAN/REN — which are
// integrated per municipality — these are single national collections on the
// DGT OGC API, so every adapter can include them: they are fetched live
// (bbox-filtered, cached), never bundled.
package srup

import (
	"context"
	"strings"

	"github.com/bernardosimoes/pdm/internal/adapter"
	"github.com/bernardosimoes/pdm/internal/model"
	"github.com/bernardosimoes/pdm/internal/source"
	"github.com/bernardosimoes/pdm/internal/spatial"
)

const itemsBase = "https://ogcapi.dgterritorio.gov.pt/collections"

// Canonical constraint types, referenced by reports and data-gap logic.
const (
	TypeZPE      = "Natura 2000 — ZPE"
	TypeZEC      = "Natura 2000 — ZEC"
	TypeIncendio = "Perigosidade de incêndio rural"
)

// Layers returns the national SRUP constraint layers. They fetch live
// regardless of opts.Live (no bundled snapshot exists at national scale);
// responses are cached like any other live fetch.
func Layers(opts source.Options) []adapter.Layer {
	opts.Live = true
	return []adapter.Layer{
		{
			ID:         "zpe",
			Title:      "Rede Natura 2000 — Zonas de Proteção Especial (ZPE)",
			Kind:       adapter.KindConstraint,
			Constraint: TypeZPE,
			Loader:     ogc("srup_zpe", "zpe", "DGT/SNIT — SRUP, Rede Natura 2000 (ZPE)", opts),
			Detail:     designacao,
		},
		{
			ID:         "zec",
			Title:      "Rede Natura 2000 — Zonas Especiais de Conservação (ZEC)",
			Kind:       adapter.KindConstraint,
			Constraint: TypeZEC,
			Loader:     ogc("srup_zec", "zec", "DGT/SNIT — SRUP, Rede Natura 2000 (ZEC)", opts),
			Detail:     designacao,
		},
		{
			ID:         "incendio",
			Title:      "Perigosidade de Incêndio Rural — classes alta e muito alta",
			Kind:       adapter.KindConstraint,
			Constraint: TypeIncendio,
			// The national chart carries every hazard class; only the high
			// classes restrict building (SGIFR), so the rest are filtered out —
			// otherwise virtually every parcel in the country would flag it.
			Loader: filtered(
				ogc("srup_perigosidade_inc_rural", "incendio", "DGT/SNIT — SRUP, Perigosidade de Incêndio Rural", opts),
				highHazard,
			),
			Detail: func(f spatial.Feature) string { return f.Prop("tipologia") },
		},
	}
}

func ogc(collection, layer, name string, opts source.Options) source.Loader {
	return source.OGC(source.OGCConfig{
		ItemsURL: itemsBase + "/" + collection + "/items",
		UseBBox:  true,
		Meta:     model.Source{Name: name, Layer: layer, URL: itemsBase + "/" + collection},
	}, opts)
}

func designacao(f spatial.Feature) string {
	return f.Prop("designacao", "sitio", "servidao")
}

// highHazard keeps only the hazard classes that actually restrict building.
func highHazard(f spatial.Feature) bool {
	t := strings.ToLower(strings.TrimSpace(f.Prop("tipologia")))
	return t == "alta" || t == "muito alta"
}

// filtered wraps a loader, keeping only the features that pass keep.
func filtered(l source.Loader, keep func(spatial.Feature) bool) source.Loader {
	return func(ctx context.Context) (source.Loaded, error) {
		loaded, err := l(ctx)
		if err != nil {
			return loaded, err
		}
		kept := loaded.Features[:0]
		for _, f := range loaded.Features {
			if keep(f) {
				kept = append(kept, f)
			}
		}
		loaded.Features = kept
		return loaded, nil
	}
}
