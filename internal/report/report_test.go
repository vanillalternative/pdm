package report

import (
	"strings"
	"testing"

	"github.com/bernardosimoes/pdm/internal/model"
)

func TestGroupThousands(t *testing.T) {
	cases := map[int64]string{
		0:       "0",
		78:      "78",
		3250:    "3,250",
		9537565: "9,537,565",
		-1234:   "-1,234",
	}
	for in, want := range cases {
		if got := group(in); got != want {
			t.Errorf("group(%d)=%q, want %q", in, got, want)
		}
	}
}

func TestArea(t *testing.T) {
	if got := area(3337.4); got != "3,337" {
		t.Errorf("area rounding: %q", got)
	}
}

func TestPct(t *testing.T) {
	if got := pct(64.63); got != "64.6" {
		t.Errorf("pct=%q", got)
	}
	if got := pct(100); got != "100.0" {
		t.Errorf("pct=%q", got)
	}
}

// TestPolygonEmptyStates: text and markdown polygon reports for a zoning-only
// result with nothing matched must print placeholders, not bare headings or a
// header-only table.
func TestPolygonEmptyStates(t *testing.T) {
	r := &model.PolygonResult{
		Municipality: "Ourém",
		Supported:    true,
		Confidence:   model.ConfidenceLow,
		Disclaimer:   model.Disclaimer,
	}
	var b strings.Builder
	if err := Polygon(&b, r, FormatText); err != nil {
		t.Fatal(err)
	}
	txt := b.String()
	if !strings.Contains(txt, "(none matched)") || !strings.Contains(txt, "(none evaluated)") {
		t.Errorf("text polygon report missing empty-state placeholders:\n%s", txt)
	}

	b.Reset()
	if err := Polygon(&b, r, FormatMarkdown); err != nil {
		t.Fatal(err)
	}
	md := b.String()
	if !strings.Contains(md, "(none matched)") || !strings.Contains(md, "(none evaluated)") {
		t.Errorf("markdown polygon report missing empty-state placeholders:\n%s", md)
	}
	if strings.Contains(md, "|---:") {
		t.Errorf("markdown polygon report renders a header-only table:\n%s", md)
	}
}

func TestPolygonMarkdownExplainsApproximateREN(t *testing.T) {
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
		Confidence: model.ConfidenceMedium,
		Disclaimer: model.Disclaimer,
	}
	var b strings.Builder
	if err := Polygon(&b, r, FormatMarkdown); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"yes (approx.)",
		"Reserva Ecológica Nacional",
		"does not identify the REN subtype",
		"Obtain the municipal REN plant/extract",
		"exact affected area and percentage were not calculated",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown REN explanation missing %q:\n%s", want, out)
		}
	}
}

func TestParseFormat(t *testing.T) {
	for _, s := range []string{"", "text", "json", "markdown", "md"} {
		if _, err := ParseFormat(s); err != nil {
			t.Errorf("ParseFormat(%q) errored: %v", s, err)
		}
	}
	if _, err := ParseFormat("xml"); err == nil {
		t.Error("ParseFormat(xml) should error")
	}
}
