// Package mafra integrates the official GeoMafra PDM 2023 ArcGIS services.
package mafra

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/bernardosimoes/pdm/internal/adapter"
	"github.com/bernardosimoes/pdm/internal/adapter/crus"
	"github.com/bernardosimoes/pdm/internal/model"
	"github.com/bernardosimoes/pdm/internal/reg"
	"github.com/bernardosimoes/pdm/internal/source"
	"github.com/bernardosimoes/pdm/internal/spatial"
)

const (
	dtcc = "1109"
	base = "https://geomafra.cm-mafra.pt/arcgisext/rest/services/PDM_2023/1A_ClassificacaoSolo/FeatureServer"
)

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) Municipality() string { return "Mafra" }

func (a *Adapter) Regulation() *reg.Store { return nil }

func (a *Adapter) BaseConfidence() model.Confidence { return model.ConfidenceMedium }

func (a *Adapter) Plan() model.PlanInfo {
	return model.PlanInfo{
		Name:          "PDM de Mafra — alteração de 2023",
		Kind:          "PDM",
		Municipality:  "Mafra",
		PublishedRef:  "Aviso n.º 5280/2023, de 13 de março, na redação em vigor",
		PublishedDate: "2023-03-13",
		Documents: []model.Document{
			{Title: "Município de Mafra — PDM em vigor", URL: "https://www.cm-mafra.pt/p/pdm"},
			{Title: "PDM de Mafra — alteração para todo o território (2023)", URL: "https://www.cm-mafra.pt/p/pdm2023"},
			{Title: "GeoMafra — SIG Municipal", URL: "https://geomafra-internet-cmmafra.hub.arcgis.com"},
		},
	}
}

func (a *Adapter) Layers(opts source.Options) []adapter.Layer {
	// Mafra has no bundled municipal snapshot. Always attempt the official
	// GeoMafra service and retain national CRUS as the resilient fallback.
	opts.Live = true
	return []adapter.Layer{{
		ID:       "ordenamento",
		Title:    "Ordenamento — classificação e qualificação do solo (GeoMafra)",
		Kind:     adapter.KindZoning,
		Loader:   source.Fallback(geoMafraZoning(opts), crus.LiveLoader(dtcc, "Mafra", opts)),
		Classify: classify,
	}}
}

func geoMafraZoning(opts source.Options) source.Loader {
	configs := []source.ArcGISFeatureConfig{
		{LayerURL: base + "/65", Meta: municipalSource("solo-rustico")},
		{LayerURL: base + "/66", Meta: municipalSource("solo-urbano")},
	}
	return func(ctx context.Context) (source.Loaded, error) {
		results := make([]source.Loaded, len(configs))
		errs := make([]error, len(configs))
		var wg sync.WaitGroup
		for i, cfg := range configs {
			wg.Add(1)
			go func(i int, cfg source.ArcGISFeatureConfig) {
				defer wg.Done()
				results[i], errs[i] = source.ArcGISFeatures(cfg, opts)(ctx)
			}(i, cfg)
		}
		wg.Wait()
		var features []spatial.Feature
		for i, err := range errs {
			if err != nil {
				return source.Loaded{}, fmt.Errorf("GeoMafra layer %d: %w", i, err)
			}
			features = append(features, results[i].Features...)
		}
		src := municipalSource("classificacao-solo")
		src.URL = base
		if len(results) > 0 {
			src.Provenance = results[0].Source.Provenance
			src.RetrievedAt = results[0].Source.RetrievedAt
		}
		return source.Loaded{Features: features, Source: src}, nil
	}
}

func municipalSource(layer string) model.Source {
	return model.Source{Name: "Município de Mafra — GeoMafra, PDM 2023", Layer: layer}
}

func classify(f spatial.Feature) adapter.Classification {
	class := f.Prop("CLASSIFICACAO", "classificacao", "classe_2021", "classe")
	sub := f.Prop("QUALIFICACAO", "qualificacao", "categoria_2021", "categoria")
	label := strings.TrimSpace(strings.Join(nonEmpty(class, sub), " — "))
	if label == "" {
		label = f.Prop("CONFRONTA", "SIMBOLOGIA", "classificacao_e_qualificacao")
	}
	return adapter.Classification{Class: class, Subclass: sub, Label: label, RawCode: f.Prop("OBJECTID", "codigo", "cod")}
}

func nonEmpty(values ...string) []string {
	var out []string
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			out = append(out, strings.TrimSpace(v))
		}
	}
	return out
}
