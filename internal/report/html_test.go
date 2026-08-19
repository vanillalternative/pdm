package report

import (
	"strings"
	"testing"

	"github.com/bernardosimoes/pdm/internal/model"
)

func TestPointHTML(t *testing.T) {
	r := &model.PointResult{
		Input:        model.Coordinate{Lat: 39.6, Lon: -8.41},
		Municipality: "Tomar",
		Supported:    true,
		Plan:         &model.PlanInfo{Name: "PDM de Tomar"},
		Zoning:       []model.ZoningHit{{Class: "Solo Urbano", Subclass: "Espaço Central", Label: "Solo Urbano – Espaço Central"}},
		Constraints: []model.ConstraintHit{
			{Type: "RAN", Present: false},
			{Type: "Albufeira Castelo de Bode", Present: true, Detail: "Área de Intervenção do POACB"},
		},
		Regulation: &model.Regulation{
			Reference: "Aviso 1510/2022",
			Articles:  []model.Article{{Number: "31.º", Title: "Identificação e usos", Section: "Espaços Centrais", Text: "corpo do artigo"}},
		},
		Sources:    []model.Source{{Name: "DGT CRUS", Provenance: model.ProvenanceBundled}},
		Confidence: model.ConfidenceMedium,
		Disclaimer: model.Disclaimer,
	}
	var b strings.Builder
	if err := PointHTML(&b, r, `<figure class="pdm-map">MAP</figure>`); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"<!doctype html>", "</html>", "PDM de Tomar", "Espaço Central",
		"Albufeira Castelo de Bode", "chip-yes", "chip-no",
		`class="pdm-map"`, "Art. 31.º", "corpo do artigo", "confidence: medium",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
}

// TestHTMLEmptyStates: a zoning-only municipality with a failed live fetch
// yields no zoning and no constraints — both sections must render an explicit
// placeholder, never an empty table or a bare heading.
func TestHTMLEmptyStates(t *testing.T) {
	pt := &model.PointResult{
		Municipality: "Ourém",
		Supported:    true,
		Plan:         &model.PlanInfo{Name: "PDM de Ourém — regime de uso do solo em vigor (CRUS)"},
		Confidence:   model.ConfidenceLow,
		Disclaimer:   model.Disclaimer,
	}
	var b strings.Builder
	if err := PointHTML(&b, pt, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "None evaluated for this municipality") {
		t.Error("point HTML missing empty-constraints placeholder")
	}

	pg := &model.PolygonResult{
		Municipality: "Ourém",
		Supported:    true,
		Confidence:   model.ConfidenceLow,
		Disclaimer:   model.Disclaimer,
	}
	b.Reset()
	if err := PolygonHTML(&b, pg, ""); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "No zoning category matched") {
		t.Error("polygon HTML missing empty-zoning placeholder")
	}
	if strings.Contains(out, "<tbody></tbody>") {
		t.Error("polygon HTML renders an empty zoning table")
	}
	if !strings.Contains(out, "None evaluated for this municipality") {
		t.Error("polygon HTML missing empty-constraints placeholder")
	}
}

func TestPolygonHTMLExplainsMunicipalInstrumentContext(t *testing.T) {
	r := &model.PolygonResult{
		Municipality:   "Almada",
		Supported:      true,
		AnalysedAreaM2: 76296,
		Constraints: []model.ConstraintHit{{
			Type:    "REN",
			Present: true,
			Detail:  "delimitação municipal da REN em vigor (AVISO 19707/2021)",
			Note:    "A fonte nacional dá a delimitação da REN sem desagregação por tipologia (erosão, cheias, aquíferos…). Presença avaliada pelo envelope do polígono (aproximação); área e percentagem não calculadas.",
		}},
		Instruments: []model.Instrument{{
			Name:    "POC-ACE — Programa da Orla Costeira Alcobaça-Cabo Espichel",
			State:   "vigor",
			Diploma: "RCM n.º 66/2019, de 11 de abril",
		}},
		Confidence: model.ConfidenceMedium,
		Disclaimer: model.Disclaimer,
	}
	var b strings.Builder
	if err := PolygonHTML(&b, r, ""); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"yes (approx.)",
		"Meaning:",
		"Reserva Ecológica Nacional",
		"Next check:",
		"municipality-level context; parcel/location overlap not confirmed",
		"do not mean the parcel is inside",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("HTML explanation missing %q:\n%s", want, out)
		}
	}
}

func TestHTMLPlainLanguageIntroExplainsFindings(t *testing.T) {
	inside := true
	r := &model.PolygonResult{
		Municipality:   "Tomar",
		Supported:      true,
		AnalysedAreaM2: 1200,
		Plan:           &model.PlanInfo{Name: "PDM de Tomar"},
		Zoning: []model.ZoningHit{{
			Class: "Solo Urbano", Subclass: "Espaços Habitacionais", Label: "Solo Urbano – Espaços Habitacionais", AreaM2: 1200, Percent: 100,
		}},
		Constraints: []model.ConstraintHit{{
			Type: "REN", Present: true, AreaM2: 400, Percent: 33.3,
		}},
		Instruments: []model.Instrument{{
			Name:        "POACB — Plano de Ordenamento da Albufeira de Castelo do Bode",
			Family:      "albufeira",
			State:       "vigor",
			Status:      "em vigor; PEACB (programa especial) em elaboração",
			PointInside: &inside,
		}},
		Regulation: &model.Regulation{Articles: []model.Article{{
			Number:  "96.º",
			Title:   "Cedências e compensações",
			Section: "Espaços Habitacionais",
			Text:    "os proprietários são obrigados a ceder à Câmara Municipal áreas para vias, estacionamento, espaços verdes e equipamentos",
		}}},
		Confidence: model.ConfidenceMedium,
		Disclaimer: model.Disclaimer,
	}
	var b strings.Builder
	if err := PolygonHTML(&b, r, ""); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"Plain-language summary",
		"REN means Reserva Ecológica Nacional",
		"reservoir/water-protection plan",
		"Article 96.º (Cedências e compensações) means",
		"give land to the municipality",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plain-language intro missing %q:\n%s", want, out)
		}
	}
}

func TestRuralIntroSkipsUrbanCessionArticleExplanation(t *testing.T) {
	r := &model.PolygonResult{
		Municipality:   "Tomar",
		Supported:      true,
		AnalysedAreaM2: 2500,
		Zoning: []model.ZoningHit{{
			Class: "Solo Rústico", Subclass: "Espaços Florestais", Label: "Solo Rústico – Espaços Florestais", AreaM2: 2500, Percent: 100,
		}},
		Regulation: &model.Regulation{Articles: []model.Article{{
			Number: "96.º",
			Title:  "Cedências e compensações",
			Text:   "os proprietários são obrigados a ceder à Câmara Municipal",
		}}},
		Confidence: model.ConfidenceMedium,
		Disclaimer: model.Disclaimer,
	}
	var b strings.Builder
	if err := PolygonHTML(&b, r, ""); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if strings.Contains(out, "Matched articles in practical terms") {
		t.Errorf("rural intro should not explain urban cession article:\n%s", out)
	}
	if !strings.Contains(out, "Art. 96.º") {
		t.Errorf("verbatim article should still be present:\n%s", out)
	}
}

func TestTomarAgriculturalComplementaryArticlesExplainNonRelevance(t *testing.T) {
	r := &model.PolygonResult{
		Municipality:   "Tomar",
		Supported:      true,
		AnalysedAreaM2: 1200,
		Zoning: []model.ZoningHit{{
			Class: "Solo Rústico", Subclass: "Espaço Agrícola", Label: "Solo Rústico – Espaços Agrícolas Complementares", AreaM2: 1200, Percent: 100,
		}},
		Constraints: []model.ConstraintHit{{
			Type: "Albufeira Castelo de Bode", Present: false, Layer: "poacb",
		}},
		Regulation: &model.Regulation{Articles: []model.Article{
			{
				Number: "52.º",
				Title:  "Regime geral de ocupação dos espaços agrícolas",
				Text:   "habitação própria e permanente do agricultor; Área mínima do prédio — 40 000 m2; Número de fogos máximo — um",
			},
			{
				Number: "54.º",
				Title:  "Uso turístico nos espaços agrícolas complementares",
				Text:   "Área mínima da parcela: 4 ha; Índice de utilização do solo máximo: 0,04",
			},
			{
				Number: "55.º",
				Title:  "Regime dos espaços agrícolas da zona de proteção da Albufeira de Castelo de Bode",
				Text:   "Nos espaços agrícolas integrados na zona de proteção da Albufeira de Castelo de Bode",
			},
		}},
		Confidence: model.ConfidenceMedium,
		Disclaimer: model.Disclaimer,
	}
	var b strings.Builder
	if err := PolygonHTML(&b, r, ""); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"farmer dwelling route requires a minimum parcel area of 4 ha (40,000 m²)",
		"this dwelling route does not fit this parcel",
		"conditional tourism rule",
		"does not make the parcel generally buildable",
		"below 4 ha",
		"not relevant unless a larger legally usable parcel area is demonstrated",
		"No POACB overlap was detected",
		"not applicable here",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Tomar A2 article explanation missing %q:\n%s", want, out)
		}
	}
}

func TestParseFormatHTML(t *testing.T) {
	f, err := ParseFormat("html")
	if err != nil || f != FormatHTML {
		t.Fatalf("ParseFormat(html) = %v, %v", f, err)
	}
}
