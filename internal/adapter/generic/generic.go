// Package generic is the fallback adapter serving any mainland municipality
// without a dedicated one. It answers zoning from the national DGT CRUS
// dataset (the harmonised carta of every municipality's plans in force) and
// the national SRUP constraints (Rede Natura 2000, rural fire hazard) —
// nothing municipality-specific is hardcoded. Because those datasets are far
// too large to bundle nationally, this adapter always fetches live (cached);
// and because the municipal constraint layers (RAN, REN, servidões locais) and
// the parsed regulation are still missing, results are capped at low
// confidence and clearly annotated. Dedicated adapters (like Tomar's) add
// those layers one municipality at a time.
package generic

import (
	"github.com/bernardosimoes/pdm/internal/adapter"
	"github.com/bernardosimoes/pdm/internal/adapter/crus"
	"github.com/bernardosimoes/pdm/internal/adapter/srup"
	"github.com/bernardosimoes/pdm/internal/model"
	"github.com/bernardosimoes/pdm/internal/reg"
	"github.com/bernardosimoes/pdm/internal/source"
)

// Adapter serves one municipality from national datasets only.
type Adapter struct {
	name string
	code string // CAOP dtmn code, used as the CRUS dtcc filter
}

// New returns the generic adapter for the named municipality; code is its CAOP
// dtmn code (may be empty — the bbox filter still bounds the query).
func New(name, code string) *Adapter { return &Adapter{name: name, code: code} }

func (a *Adapter) Municipality() string { return a.name }

// Regulation returns nil: the written regulation is parsed per municipality by
// dedicated adapters, never generically.
func (a *Adapter) Regulation() *reg.Store { return nil }

func (a *Adapter) BaseConfidence() model.Confidence {
	// The zoning (DGT CRUS) and national servidões (SRUP) are authoritative,
	// but the answer is still partial: the municipal constraint layers (RAN,
	// REN, servidões locais) and the regulation are not checked, so the
	// overall result is capped at low.
	return model.ConfidenceLow
}

func (a *Adapter) Plan() model.PlanInfo {
	return model.PlanInfo{
		Name:         "PDM de " + a.name + " — regime de uso do solo em vigor (CRUS)",
		Kind:         "PDM",
		Municipality: a.name,
		Documents: []model.Document{
			{
				Title: "DGT — CRUS (Carta do Regime de Uso do Solo)",
				URL:   crus.CollectionURL,
				Note:  "Classificação e qualificação do solo dos planos em vigor, harmonizada a nível nacional.",
			},
			{
				Title: "PCGT/DGT — Plataforma Colaborativa de Gestão Territorial",
				URL:   "https://pcgt.dgterritorio.gov.pt",
				Note:  "Pesquise aqui o plano do município para a publicação oficial (Diário da República) e o regulamento.",
			},
			{
				Title: "SNIT — Sistema Nacional de Informação Territorial",
				URL:   "https://snit.dgterritorio.gov.pt",
			},
		},
	}
}

func (a *Adapter) Layers(opts source.Options) []adapter.Layer {
	// There is no bundled snapshot for this municipality, so live fetching
	// (bbox-limited, cached) is the only way to answer — regardless of --live.
	opts.Live = true
	layers := []adapter.Layer{
		{
			ID:       "ordenamento",
			Title:    "Ordenamento — classificação e qualificação do solo (CRUS/PDM)",
			Kind:     adapter.KindZoning,
			Loader:   crus.LiveLoader(a.code, a.name, opts),
			Classify: crus.Classify,
		},
	}
	return append(layers, srup.Layers(opts)...)
}
