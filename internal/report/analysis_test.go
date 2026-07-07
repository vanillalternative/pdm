package report

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bernardosimoes/pdm/internal/ai"
	"github.com/bernardosimoes/pdm/internal/model"
)

func analysisFixtures() (AnalysisView, *ai.Analysis, []string) {
	v := NewPolygonAnalysisView(&model.PolygonResult{
		Municipality:   "Tomar",
		Freguesia:      "Tomar (São João Baptista)",
		Supported:      true,
		Plan:           &model.PlanInfo{Name: "PDM de Tomar", Documents: []model.Document{{Title: "Regulamento", URL: "https://dre.pt/x", Note: "DR ref."}}},
		AnalysedAreaM2: 1234.5,
		Zoning:         []model.ZoningHit{{Label: "Solo rústico — Espaços agrícolas", AreaM2: 1234.5, Percent: 100}},
		Constraints:    []model.ConstraintHit{{Type: "REN", Present: true, AreaM2: 600, Percent: 48.6}},
		Regulation:     &model.Regulation{Articles: []model.Article{{Number: "31.º", Title: "Espaços agrícolas", Text: "Texto integral."}}},
		Confidence:     model.ConfidenceMedium,
		Notes:          []string{"Nota do motor de análise."},
		Disclaimer:     model.Disclaimer,
	})
	a := &ai.Analysis{
		Title:            "Análise urbanística — Tomar",
		ExecutiveSummary: "Parcela condicionada pela REN. <script>alert(1)</script>",
		ViabilitySignal:  ai.ViabilityCondicionado,
		Sections: []ai.Section{
			{ID: "identificacao", Heading: "Identificação do terreno", Reading: "Primeiro parágrafo.\n\nSegundo parágrafo.", Notes: "Nota legal."},
			{ID: "grau_incerteza", Heading: "Grau de incerteza", Reading: "Incerteza média.", Notes: ""},
		},
		Citations:     []ai.Citation{{ArticleNumber: "31.º", Relevance: "Regime aplicável"}},
		Uncertainties: []string{"Sem dados de topografia."},
	}
	gaps := []string{"Sem dados sobre acessos e infraestruturas."}
	return v, a, gaps
}

func TestAnalysisHTML(t *testing.T) {
	v, a, gaps := analysisFixtures()
	var buf bytes.Buffer
	if err := AnalysisHTML(&buf, v, a, gaps, `<figure class="pdm-map">MAP</figure>`, "análise AI · basic · claude-haiku-4-5"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"<!doctype html>",
		"v-condicionado",            // viability banner class
		"Condicionado",              // viability label
		"claude-haiku-4-5",          // tier badge
		"Localização",               // deterministic facts
		"Tomar (São João Baptista)", // freguesia row
		"1,235 m²",                  // area from Go data, rounded/grouped
		"Identificação do terreno",  // AI section
		"Segundo parágrafo.",        // paragraph splitting
		"Nota legal.",               // section notes
		`href="#art-31.º"`,          // citation deep-link…
		`id="art-31.º"`,             // …to the verbatim article
		"Legislação e documentos",   // plan documents
		"Regulamento aplicável",     // pt-PT regulation heading
		"confiança: média",          // pt-PT confidence chip
		"Grau de incerteza e limitações",
		"Sem dados sobre acessos",               // deterministic gap rendered
		"Nota do motor de análise.",             // engine notes merged in
		"gerado por um modelo de IA",            // AI disclaimer
		"NOT an official municipal certificate", // standard disclaimer
		`class="pdm-map"`,                       // map embedded
	} {
		if !strings.Contains(out, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
	// AI prose is untrusted: script must come out escaped.
	if strings.Contains(out, "<script>") {
		t.Error("AI text was not escaped")
	}
	// Sections render in the (already canonical) given order.
	if strings.Index(out, "Identificação do terreno") > strings.Index(out, "Incerteza média.") {
		t.Error("sections out of order")
	}
}

func TestAnalysisHTMLPointView(t *testing.T) {
	v, a, gaps := analysisFixtures()
	v.IsPolygon = false
	v.AreaM2 = 0
	v.Coord = &model.Coordinate{Lat: 39.6, Lon: -8.41}
	var buf bytes.Buffer
	if err := AnalysisHTML(&buf, v, a, gaps, "", "tier"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "39.600000, -8.410000") {
		t.Error("point coordinates missing from Localização")
	}
	if strings.Contains(out, "Área analisada") {
		t.Error("polygon-only row rendered for a point view")
	}
}

func TestAnalysisMarkdown(t *testing.T) {
	v, a, gaps := analysisFixtures()
	var buf bytes.Buffer
	if err := AnalysisMarkdown(&buf, v, a, gaps, "análise AI · basic"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"# Análise urbanística — Tomar",
		"**Condicionado**",
		"## Síntese",
		"### Identificação do terreno",
		"> Nota legal.",
		"## Artigos citados na análise",
		"**Art. 31.º**",
		"## Grau de incerteza e limitações",
		"gerado por um modelo de IA",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q", want)
		}
	}
}
