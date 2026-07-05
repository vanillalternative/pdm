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

func TestParseFormatHTML(t *testing.T) {
	f, err := ParseFormat("html")
	if err != nil || f != FormatHTML {
		t.Fatalf("ParseFormat(html) = %v, %v", f, err)
	}
}
