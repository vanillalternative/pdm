// Package ourem is the per-municipality adapter for Ourém. The municipal
// WebSIG exposes the PDM layers through QGIS Server WMS/GetFeatureInfo; WFS
// feature export is not advertised for these layers, so geometry support is
// point/probe based.
package ourem

import (
	"strings"

	"github.com/bernardosimoes/pdm/internal/adapter"
	"github.com/bernardosimoes/pdm/internal/adapter/crus"
	"github.com/bernardosimoes/pdm/internal/model"
	"github.com/bernardosimoes/pdm/internal/reg"
	"github.com/bernardosimoes/pdm/internal/source"
	"github.com/bernardosimoes/pdm/internal/spatial"
)

const websigWMS = "https://websig.cm-ourem.pt/cgi-bin/qgis_mapserv.fcgi?map=/var/www/projetos/visualizadores/eploc.qgz"

// Adapter serves the municipality of Ourém.
type Adapter struct{}

// New returns the Ourém adapter.
func New() *Adapter { return &Adapter{} }

func (a *Adapter) Municipality() string { return "Ourém" }

func (a *Adapter) Regulation() *reg.Store { return nil }

func (a *Adapter) BaseConfidence() model.Confidence {
	return model.ConfidenceMedium
}

func (a *Adapter) Plan() model.PlanInfo {
	return model.PlanInfo{
		Name:          "PDM de Ourém",
		Kind:          "PDM",
		Municipality:  "Ourém",
		PublishedRef:  "Aviso n.º 10844/2020",
		PublishedDate: "2020-07-23",
		Documents: []model.Document{
			{
				Title: "PDM de Ourém — Regulamento (Aviso n.º 10844/2020)",
				URL:   "https://www.ourem.pt/wp-content/uploads/2020/07/RegulamentoAviso10844_2020.pdf",
			},
			{
				Title: "Município de Ourém — Plano Diretor Municipal em vigor",
				URL:   "https://www.ourem.pt/areas-de-acao/informacao-geografica/planos-em-vigor/plano-diretor-municipal/",
				Note:  "Peças desenhadas, condicionantes, DWG/PDF e ligação ao WebSIG municipal.",
			},
			{
				Title: "WebSIG do Município de Ourém",
				URL:   "https://websig.cm-ourem.pt",
				Note:  "QGIS Server WMS com camadas de ordenamento e condicionantes.",
			},
			{
				Title: "PCGT/DGT — ficha oficial do PDM de Ourém",
				URL:   "https://pcgt.dgterritorio.gov.pt/FDE9796",
			},
		},
	}
}

func (a *Adapter) Layers(opts source.Options) []adapter.Layer {
	crusOpts := opts
	crusOpts.Live = true
	zoning := adapter.Layer{
		ID:       "ordenamento",
		Title:    "Ordenamento — classificação e qualificação do solo (WebSIG municipal)",
		Kind:     adapter.KindZoning,
		Loader:   source.Fallback(source.WMSFeatureInfo(wmsCfg("Município de Ourém WebSIG — Ordenamento", "pdm_ordenamento"), opts), crus.LiveLoader("1421", "Ourém", crusOpts)),
		Classify: classifyOrdenamento,
	}
	if !opts.Live {
		zoning.Title = "Ordenamento — classificação e qualificação do solo (CRUS/PDM)"
		zoning.Loader = crus.LiveLoader("1421", "Ourém", crusOpts)
		zoning.Classify = crus.Classify
		return []adapter.Layer{zoning}
	}
	return []adapter.Layer{
		{
			ID:       "ordenamento",
			Title:    "Ordenamento — classificação e qualificação do solo (WebSIG municipal)",
			Kind:     adapter.KindZoning,
			Loader:   zoning.Loader,
			Classify: classifyOrdenamento,
		},
		constraint(opts, "ran", "RAN", "RAN — Reserva Agrícola Nacional (WebSIG municipal)", "pdm_ran", nil),
		constraint(opts, "ren", "REN", "REN — Reserva Ecológica Nacional (WebSIG municipal)", "pdm_ren", nil),
		constraint(opts, "incendio-perigosidade", "Perigosidade de incêndio", "Perigosidade de incêndio rural", "pdm_pif", propDetail("Classe", "gridcode", "perigosida", "Perigosidade")),
		constraint(opts, "dominio-hidrico", "Domínio hídrico", "Domínio hídrico", "pdm_dom_hidrico_pol", nil),
		constraint(opts, "captacoes-agua", "Captações de água", "Perímetros de proteção das captações de água subterrâneas", "pdm_perim_prot_capt_agua_sub_pol", nil),
		constraint(opts, "sic", "Rede Natura 2000 — SIC", "Sítio de Interesse Comunitário da Rede Natura 2000", "pdm_sic", nil),
		constraint(opts, "pnsac", "Parque Natural", "Parque Natural da Serra de Aire e dos Candeeiros", "pdm_pnsac", nil),
		constraint(opts, "eem", "Estrutura ecológica municipal", "Estrutura ecológica municipal", "pdm_eem", nil),
		constraint(opts, "zac", "Zonas ameaçadas pelas cheias", "Zonas ameaçadas pelas cheias", "pdm_zac", nil),
		constraint(opts, "instabilidade-vertentes", "Instabilidade de vertentes", "Áreas de instabilidade de vertentes", "pdm_aiv_pol", nil),
		constraint(opts, "zonamento-acustico", "Zonamento acústico", "Zonamento acústico", "pdm_zon_acustico_pol", nil),
	}
}

func wmsCfg(name, layer string) source.WMSFeatureInfoConfig {
	return source.WMSFeatureInfoConfig{
		BaseURL: websigWMS,
		Layer:   layer,
		Meta: model.Source{
			Name:  name,
			Layer: layer,
		},
	}
}

func constraint(opts source.Options, id, typ, title, layer string, detail func([]spatial.Feature) source.Interpretation) adapter.Layer {
	return adapter.Layer{
		ID:         id,
		Title:      title,
		Kind:       adapter.KindConstraint,
		Constraint: typ,
		Probe:      source.WMSFeatureInfoProbe(wmsCfg("Município de Ourém WebSIG — "+title, layer), opts, detail),
	}
}

func classifyOrdenamento(f spatial.Feature) adapter.Classification {
	class := f.Prop("Solo")
	sub := f.Prop("Tipo de Solo")
	if st := f.Prop("Subtipo"); st != "" {
		if sub == "" {
			sub = st
		} else {
			sub += " — " + st
		}
	}
	if class == "" {
		class = f.Prop("classe_2021", "classe")
	}
	if sub == "" {
		sub = f.Prop("categoria_2021", "categoria", "classificacao_e_qualificacao")
	}
	label := strings.TrimSpace(class)
	if sub != "" {
		if label == "" {
			label = sub
		} else {
			label += " — " + sub
		}
	}
	if label == "" {
		label = f.Prop("Tipo de Solo", "Solo")
	}
	return adapter.Classification{Class: class, Subclass: sub, Label: label, RawCode: f.Prop("Tipo de Solo", "Subtipo")}
}

func propDetail(keys ...string) func([]spatial.Feature) source.Interpretation {
	return func(hits []spatial.Feature) source.Interpretation {
		if len(hits) == 0 {
			return source.Interpretation{}
		}
		for _, k := range keys {
			if v := hits[0].Prop(k); v != "" {
				return source.Interpretation{Present: true, Detail: v}
			}
		}
		return source.Interpretation{Present: true}
	}
}
