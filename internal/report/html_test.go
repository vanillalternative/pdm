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

func TestParseFormatHTML(t *testing.T) {
	f, err := ParseFormat("html")
	if err != nil || f != FormatHTML {
		t.Fatalf("ParseFormat(html) = %v, %v", f, err)
	}
}
