// Package tomar is the per-municipality adapter for Tomar (the pilot). It
// declares the PDM in force, the planning layers to query, and how to interpret
// each layer's features. Layers load from a bundled real snapshot by default;
// in live mode zoning and RAN attempt the official DGT OGC API first and fall
// back to the bundle.
package tomar

import (
	"github.com/bernardosimoes/pdm/data"
	"github.com/bernardosimoes/pdm/internal/adapter"
	"github.com/bernardosimoes/pdm/internal/adapter/crus"
	"github.com/bernardosimoes/pdm/internal/model"
	"github.com/bernardosimoes/pdm/internal/reg"
	"github.com/bernardosimoes/pdm/internal/source"
	"github.com/bernardosimoes/pdm/internal/spatial"
)

const (
	ogc  = "https://ogcapi.dgterritorio.gov.pt/collections"
	dtcc = "1418" // CAOP dtmn code for Tomar
)

// Adapter serves the municipality of Tomar.
type Adapter struct{}

// New returns the Tomar adapter.
func New() *Adapter { return &Adapter{} }

func (a *Adapter) Municipality() string { return "Tomar" }

// Regulation returns nil: Tomar's parsed regulamento is served data-driven from
// the shared regstore (data/regulamentos/1418.json), keyed by its dtcc code, so
// there is a single path for every municipality's regulation.
func (a *Adapter) Regulation() *reg.Store { return nil }

func (a *Adapter) BaseConfidence() model.Confidence {
	// Zoning and RAN come from the national DGT datasets (CRUS/SRUP); REN from
	// the municipal geoportal. Data is authoritative but derived/generalised and
	// the query is automated, so results are capped at medium.
	return model.ConfidenceMedium
}

func (a *Adapter) Plan() model.PlanInfo {
	return model.PlanInfo{
		Name:          "PDM de Tomar",
		Kind:          "PDM",
		Municipality:  "Tomar",
		PublishedRef:  "Aviso n.º 1510/2022, DR 2.ª série, n.º 16, de 2022-01-24",
		PublishedDate: "2022-01-25",
		Documents: []model.Document{
			{
				Title: "PDM de Tomar — Regulamento e plano (Diário da República, Aviso n.º 1510/2022)",
				URL:   "https://files.dre.pt/2s/2022/01/016000000/0032700390.pdf",
				Note:  "Publicação oficial em vigor desde 2022-01-25.",
			},
			{
				Title: "PCGT/DGT — ficha oficial do PDM de Tomar (metadados e peças do plano)",
				URL:   "https://pcgt.dgterritorio.gov.pt/FDE12471",
			},
			{
				Title: "Geoportal do Município de Tomar / Médio Tejo (MuniSIG)",
				URL:   "https://geoportal.mediotejo.pt/MuniSIG/Tomar/",
				Note:  "Plantas de Ordenamento e Condicionantes municipais.",
			},
		},
	}
}

// bundledMeta describes a bundled real snapshot layer.
func bundledMeta(name, layer string) model.Source {
	return model.Source{
		Name:       name,
		Layer:      layer,
		Provenance: model.ProvenanceBundled,
	}
}

func (a *Adapter) Layers(opts source.Options) []adapter.Layer {
	return []adapter.Layer{
		{
			ID:       "ordenamento",
			Title:    "Ordenamento — classificação e qualificação do solo (CRUS/PDM)",
			Kind:     adapter.KindZoning,
			Loader:   ordenamentoLoader(opts),
			Classify: crus.Classify,
		},
		{
			ID:         "ran",
			Title:      "RAN — Reserva Agrícola Nacional",
			Kind:       adapter.KindConstraint,
			Constraint: "RAN",
			Loader:     ranLoader(opts),
			Detail:     ranDetail,
		},
		{
			ID:         "ren",
			Title:      "REN — Reserva Ecológica Nacional",
			Kind:       adapter.KindConstraint,
			Constraint: "REN",
			Loader:     source.Bundled(data.TomarREN, bundledMeta("Município de Tomar (MuniSIG) — REN", "ren")),
			Detail:     renDetail,
		},
		{
			ID:         "poacb",
			Title:      "Albufeira de Castelo de Bode — Área de Intervenção do POACB",
			Kind:       adapter.KindConstraint,
			Constraint: "Albufeira Castelo de Bode",
			Loader:     source.Bundled(data.TomarPOACB, bundledMeta("Município de Tomar (MuniSIG) — POACB", "poacb")),
			Detail:     func(f spatial.Feature) string { return f.Prop("designacao", "servidao") },
		},
	}
}

// ordenamentoLoader: live DGT CRUS (dtcc+bbox filtered) first, then bundled
// snapshot.
func ordenamentoLoader(opts source.Options) source.Loader {
	bundled := source.Bundled(data.TomarOrdenamento, bundledMeta("DGT CRUS — ordenamento do solo (Tomar)", "ordenamento"))
	if !opts.Live {
		return bundled
	}
	return source.Fallback(crus.LiveLoader(dtcc, "Tomar", opts), bundled)
}

// ranLoader: live DGT SRUP RAN (municipio filter) first, then bundled snapshot.
func ranLoader(opts source.Options) source.Loader {
	bundled := source.Bundled(data.TomarRAN, bundledMeta("DGT SRUP — RAN (Tomar)", "ran"))
	if !opts.Live {
		return bundled
	}
	live := source.OGC(source.OGCConfig{
		ItemsURL: ogc + "/srup_ran/items",
		Params:   map[string]string{"municipio": "TOMAR"},
		Meta:     model.Source{Name: "DGT/SNIT — SRUP, RAN", Layer: "ran"},
	}, opts)
	return source.Fallback(live, bundled)
}

func ranDetail(f spatial.Feature) string {
	return f.Prop("designacao", "servidao", "tipologia")
}

func renDetail(f spatial.Feature) string {
	return f.Prop("tipologia", "Tipologias", "servidao")
}
