// Package tomar is the per-municipality adapter for Tomar (the pilot). It
// declares the PDM in force, the planning layers to query, and how to interpret
// each layer's features. Layers load from a bundled real snapshot by default;
// in live mode zoning and RAN attempt the official DGT OGC API first and fall
// back to the bundle.
package tomar

import (
	"github.com/bernardosimoes/pdm/data"
	"github.com/bernardosimoes/pdm/internal/adapter"
	"github.com/bernardosimoes/pdm/internal/model"
	"github.com/bernardosimoes/pdm/internal/source"
	"github.com/bernardosimoes/pdm/internal/spatial"
)

const ogc = "https://ogcapi.dgterritorio.gov.pt/collections"

// Adapter serves the municipality of Tomar.
type Adapter struct{}

// New returns the Tomar adapter.
func New() *Adapter { return &Adapter{} }

func (a *Adapter) Municipality() string { return "Tomar" }

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
			Classify: classifyOrdenamento,
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
	}
}

// ordenamentoLoader: live DGT CRUS (bbox-filtered) first, then bundled snapshot.
func ordenamentoLoader(opts source.Options) source.Loader {
	bundled := source.Bundled(data.TomarOrdenamento, bundledMeta("DGT CRUS — ordenamento do solo (Tomar)", "ordenamento"))
	if !opts.Live {
		return bundled
	}
	live := source.OGC(source.OGCConfig{
		ItemsURL: ogc + "/crus/items",
		UseBBox:  true,
		Meta:     model.Source{Name: "DGT/SNIT — CRUS (ordenamento do solo)", Layer: "ordenamento"},
	}, opts)
	return source.Fallback(live, bundled)
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

// classifyOrdenamento interprets a CRUS/PDM zoning feature.
func classifyOrdenamento(f spatial.Feature) adapter.Classification {
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

func ranDetail(f spatial.Feature) string {
	return f.Prop("designacao", "servidao", "tipologia")
}

func renDetail(f spatial.Feature) string {
	return f.Prop("tipologia", "Tipologias", "servidao")
}
