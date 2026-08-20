// Package pombal integrates the public vector layers published by the
// Municipality of Pombal's Pombal Geografico WebSIG.
package pombal

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
	dtcc = "1015"
	base = "https://sig.cm-pombal.pt/arcgis/rest/services/PDM2014"
)

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) Municipality() string { return "Pombal" }

func (a *Adapter) Regulation() *reg.Store { return nil }

func (a *Adapter) BaseConfidence() model.Confidence { return model.ConfidenceMedium }

func (a *Adapter) Plan() model.PlanInfo {
	return model.PlanInfo{
		Name:          "PDM de Pombal — 1.ª revisão, na redação em vigor",
		Kind:          "PDM",
		Municipality:  "Pombal",
		PublishedRef:  "Aviso n.º 310/2024, de 8 de janeiro, na redação em vigor",
		PublishedDate: "2024-01-09",
		Documents: []model.Document{
			{
				Title: "Município de Pombal — Plano Diretor Municipal",
				URL:   "https://www.cm-pombal.pt/servicos/ordenamento-de-territorio/plano-diretor-municipal",
				Note:  "Regulamento e plantas do PDM em vigor, incluindo alterações por adaptação.",
			},
			{
				Title: "Pombal Geográfico — Ordenamento do Território",
				URL:   "https://sig.cm-pombal.pt/MuniSIG/Html5Viewer/index.html?viewer=OrdenamentoTerritorioExt.OrdTerritorio",
				Note:  "WebSIG municipal que publica as delimitações vetoriais consultadas pelo CLI.",
			},
			{
				Title: "Diário da República — Aviso n.º 310/2024",
				URL:   "https://diariodarepublica.pt/dr/detalhe/aviso/310-2024-836161972",
			},
			{
				Title: "PCGT/DGT — ficha oficial do PDM de Pombal",
				URL:   "https://pcgt.dgterritorio.gov.pt/FDE12395",
			},
		},
	}
}

func (a *Adapter) Layers(opts source.Options) []adapter.Layer {
	// There is no bundled municipal snapshot. Query the public ArcGIS service
	// in normal and --live operation, retaining CRUS as the zoning fallback.
	opts.Live = true
	return []adapter.Layer{
		{
			ID:       "ordenamento",
			Title:    "Ordenamento — classificação e qualificação do solo (Pombal Geográfico)",
			Kind:     adapter.KindZoning,
			Loader:   source.Fallback(municipalZoning(opts), crus.LiveLoader(dtcc, "Pombal", opts)),
			Classify: classifyZoning,
		},
		constraint(opts, "ran", "RAN", "RAN — Reserva Agrícola Nacional", "PG_RAN", 54, nil),
		constraint(opts, "ren", "REN", "REN — Reserva Ecológica Nacional", "PG_REN", 57, propDetail("tipologia")),
		constraint(opts, "eem", "Estrutura ecológica municipal", "Estrutura Ecológica Municipal", "PG_EstruturaEcologica", 20, propDetail("descricao")),
		constraint(opts, "zonas-inundaveis", "Zona inundável", "Zonas Inundáveis", "PO_ClassificacaoSolo", 32, propDetail("descricao", "designacao")),
		constraint(opts, "movimentos-massa", "Suscetibilidade a movimentos de massa", "Suscetibilidade a Movimentos de Massa em Vertentes", "PG_RecursosGeologicos", 62, propDetail("risco")),
		constraint(opts, "zonamento-acustico", "Zonamento acústico", "Zonamento Acústico", "PG_ZonamentoAcustico", 65, propDetail("zona_ruido")),
		constraint(opts, "captacoes-agua", "Proteção de captações de água", "Perímetros de Proteção de Captações de Água Subterrânea", "PG_CondicionantesGerais", 71, propDetail("descricao", "designacao", "nome", "categoria")),
		constraint(opts, "protecao-patrimonio", "Proteção do património", "Zonas Gerais e Especiais de Proteção do Património", "PG_CondicionantesGerais", 140, propDetail("descric", "ref", "diplomas")),
	}
}

type zoningConfig struct {
	layer int
	class string
}

// The municipal map publishes each top-level soil class as a separate feature
// layer. Merge them into the single zoning layer expected by the query engine,
// tagging each feature so classification is not guessed from its description.
func municipalZoning(opts source.Options) source.Loader {
	configs := []zoningConfig{
		{layer: 5, class: "Solo urbano"},
		{layer: 6, class: "Solo rústico — Aglomerado rural"},
		{layer: 7, class: "Solo rústico — Área de edificação dispersa"},
		{layer: 8, class: "Solo rústico"},
	}
	return func(ctx context.Context) (source.Loaded, error) {
		results := make([]source.Loaded, len(configs))
		errs := make([]error, len(configs))
		var wg sync.WaitGroup
		for i, cfg := range configs {
			wg.Add(1)
			go func(i int, cfg zoningConfig) {
				defer wg.Done()
				results[i], errs[i] = source.ArcGISFeatures(source.ArcGISFeatureConfig{
					LayerURL:  fmt.Sprintf("%s/PO_ClassificacaoSolo/MapServer/%d", base, cfg.layer),
					OutFields: []string{"descricao", "objectid"},
					Meta:      municipalSource("classificacao-qualificacao-solo"),
				}, opts)(ctx)
			}(i, cfg)
		}
		wg.Wait()

		var features []spatial.Feature
		for i, err := range errs {
			if err != nil {
				return source.Loaded{}, fmt.Errorf("Pombal Geografico zoning layer %d: %w", configs[i].layer, err)
			}
			for j := range results[i].Features {
				if results[i].Features[j].Props == nil {
					results[i].Features[j].Props = map[string]any{}
				}
				results[i].Features[j].Props["pdm_class"] = configs[i].class
			}
			features = append(features, results[i].Features...)
		}

		src := municipalSource("classificacao-qualificacao-solo")
		src.URL = base + "/PO_ClassificacaoSolo/MapServer"
		if len(results) > 0 {
			src.Provenance = results[0].Source.Provenance
			src.RetrievedAt = results[0].Source.RetrievedAt
		}
		return source.Loaded{Features: features, Source: src}, nil
	}
}

func constraint(opts source.Options, id, typ, title, service string, layer int, detail func(spatial.Feature) string) adapter.Layer {
	return adapter.Layer{
		ID:         id,
		Title:      title + " (Pombal Geográfico)",
		Kind:       adapter.KindConstraint,
		Constraint: typ,
		Loader: source.ArcGISFeatures(source.ArcGISFeatureConfig{
			LayerURL: fmt.Sprintf("%s/%s/MapServer/%d", base, service, layer),
			Meta:     municipalSource(id),
		}, opts),
		Detail: detail,
	}
}

func municipalSource(layer string) model.Source {
	return model.Source{Name: "Município de Pombal — Pombal Geográfico, PDM", Layer: layer}
}

func classifyZoning(f spatial.Feature) adapter.Classification {
	class := f.Prop("pdm_class")
	sub := f.Prop("descricao", "categoria_2021", "categoria", "classificacao_e_qualificacao")
	label := strings.TrimSpace(strings.Join(nonEmpty(class, sub), " — "))
	return adapter.Classification{Class: class, Subclass: sub, Label: label, RawCode: f.Prop("objectid", "codigo", "cod")}
}

func propDetail(keys ...string) func(spatial.Feature) string {
	return func(f spatial.Feature) string { return f.Prop(keys...) }
}

func nonEmpty(values ...string) []string {
	var out []string
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}
