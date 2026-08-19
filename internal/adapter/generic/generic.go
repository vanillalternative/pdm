// Package generic is the fallback adapter serving any mainland municipality
// without a dedicated one. It answers zoning from the national DGT CRUS
// dataset (the harmonised carta of every municipality's plans in force) —
// nothing municipality-specific is hardcoded. Because CRUS is far too large to
// bundle nationally, this adapter always fetches live (cached). The national
// constraint layers (RAN, REN, Natura 2000, áreas protegidas, albufeiras, orla
// costeira) are not declared here — the query engine composes them for every
// municipality, dedicated or not (see internal/national). What a dedicated
// adapter still adds is the municipality's own condicionantes and the parsed
// written regulation.
package generic

import (
	"github.com/bernardosimoes/pdm/internal/adapter"
	"github.com/bernardosimoes/pdm/internal/adapter/crus"
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
	// Zoning is authoritative (DGT CRUS) and the national constraint layers
	// (RAN, REN, Natura 2000, áreas protegidas, albufeiras, orla costeira) are
	// probed live for every municipality — comparable to a dedicated adapter,
	// minus the municipality's own condicionantes and the parsed regulation, so
	// medium is the ceiling. Data gaps (e.g. REN missing from SNIT) downgrade
	// further at query time.
	return model.ConfidenceMedium
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
	return []adapter.Layer{
		{
			ID:       "ordenamento",
			Title:    "Ordenamento — classificação e qualificação do solo (CRUS/PDM)",
			Kind:     adapter.KindZoning,
			Loader:   crus.LiveLoader(a.code, a.name, opts),
			Classify: crus.Classify,
		},
	}
}
