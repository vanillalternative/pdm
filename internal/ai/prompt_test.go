package ai

import (
	"strings"
	"testing"

	"github.com/bernardosimoes/pdm/internal/model"
)

func fixtureResult() *model.PointResult {
	return &model.PointResult{
		Input:        model.Coordinate{Lat: 39.60, Lon: -8.41},
		Municipality: "Tomar",
		Supported:    true,
		Plan:         &model.PlanInfo{Name: "PDM de Tomar", Kind: "PDM", Municipality: "Tomar"},
		Zoning:       []model.ZoningHit{{Class: "Solo rústico", Subclass: "Espaços agrícolas", Label: "Solo rústico — Espaços agrícolas", Layer: "ordenamento"}},
		Constraints: []model.ConstraintHit{
			{Type: "RAN", Label: "Reserva Agrícola Nacional", Present: false, Layer: "ran"},
			{Type: "REN", Label: "Reserva Ecológica Nacional", Present: true, Layer: "ren"},
			{Type: "Rede Natura 2000 (ZEC)", Label: "Rede Natura 2000 — ZEC", Present: false, Layer: "zec"},
			{Type: "Perigosidade de incêndio rural", Label: "Perigosidade de Incêndio Rural", Present: false, Layer: "incendio"},
		},
		Regulation: &model.Regulation{
			Reference: "Aviso n.º 1/2020",
			Articles:  []model.Article{{Number: "31.º", Title: "Espaços agrícolas", Text: "Nos espaços agrícolas..."}},
		},
		Confidence: model.ConfidenceMedium,
		Disclaimer: model.Disclaimer,
	}
}

func TestBuildPointPayload(t *testing.T) {
	p, err := BuildPointPayload(fixtureResult())
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != "point" {
		t.Errorf("kind = %q, want point", p.Kind)
	}
	if len(p.ArticleNumbers) != 1 || p.ArticleNumbers[0] != "31.º" {
		t.Errorf("article numbers = %v", p.ArticleNumbers)
	}
	raw := string(p.Result)
	for _, want := range []string{"Tomar", "PDM de Tomar", "Nos espaços agrícolas", "REN"} {
		if !strings.Contains(raw, want) {
			t.Errorf("payload result missing %q", want)
		}
	}
	// Every constraint family was evaluated and regulation is present, so only
	// the always-on gap remains.
	if len(p.DataGaps) != 1 || !strings.Contains(p.DataGaps[0], "acessos") {
		t.Errorf("unexpected data gaps: %v", p.DataGaps)
	}
}

// TestBuildPayloadPartialConstraintGaps: national-only coverage (the generic
// adapter) flags the municipal gap; missing national layers flag theirs.
func TestBuildPayloadPartialConstraintGaps(t *testing.T) {
	r := fixtureResult()
	// Generic-adapter shape: only the national layers were evaluated.
	r.Constraints = []model.ConstraintHit{
		{Type: "Rede Natura 2000 (ZPE)", Present: false},
		{Type: "Rede Natura 2000 (ZEC)", Present: false},
		{Type: "Perigosidade de incêndio rural", Present: true},
	}
	p, err := BuildPointPayload(r)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(p.DataGaps, "\n")
	if !strings.Contains(joined, "Condicionantes municipais") {
		t.Errorf("expected municipal-constraints gap, got %v", p.DataGaps)
	}
	if strings.Contains(joined, "Condicionantes nacionais") {
		t.Errorf("national constraints were evaluated — no national gap expected, got %v", p.DataGaps)
	}

	// Tomar-offline shape: municipal layers evaluated, national ones not.
	r.Constraints = []model.ConstraintHit{
		{Type: "RAN", Present: false},
		{Type: "REN", Present: true},
	}
	p, err = BuildPointPayload(r)
	if err != nil {
		t.Fatal(err)
	}
	joined = strings.Join(p.DataGaps, "\n")
	if !strings.Contains(joined, "Condicionantes nacionais") {
		t.Errorf("expected national-constraints gap, got %v", p.DataGaps)
	}
	if strings.Contains(joined, "Condicionantes municipais") {
		t.Errorf("municipal constraints were evaluated — no municipal gap expected, got %v", p.DataGaps)
	}
}

// TestBuildPayloadUnknownConstraintGaps: a probe that answered "unknown" is a
// gap (the source has no data for this municipality), never an evaluation.
func TestBuildPayloadUnknownConstraintGaps(t *testing.T) {
	r := fixtureResult()
	r.Constraints = []model.ConstraintHit{
		{Type: "RAN", Present: false},
		{Type: "REN", Unknown: true},
		{Type: "Rede Natura 2000 (ZPE)", Present: false},
		{Type: "Rede Natura 2000 (ZEC)", Present: false},
		{Type: "Perigosidade de incêndio rural", Present: false},
	}
	p, err := BuildPointPayload(r)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(p.DataGaps, "\n")
	if !strings.Contains(joined, "REN: sem dados na fonte nacional") {
		t.Errorf("expected an explicit REN unknown gap, got %v", p.DataGaps)
	}
	if !strings.Contains(joined, "Condicionantes municipais") {
		t.Errorf("REN unknown must not count as evaluated, got %v", p.DataGaps)
	}
	if strings.Contains(joined, "Condicionantes nacionais") {
		t.Errorf("national constraints were evaluated — no national gap expected, got %v", p.DataGaps)
	}
}

func TestBuildPayloadDataGaps(t *testing.T) {
	r := fixtureResult()
	r.Constraints = nil
	r.Regulation = nil
	p, err := BuildPointPayload(r)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(p.DataGaps, "\n")
	for _, want := range []string{"Condicionantes", "Regulamento", "acessos"} {
		if !strings.Contains(joined, want) {
			t.Errorf("data gaps missing %q in:\n%s", want, joined)
		}
	}
	if len(p.ArticleNumbers) != 0 {
		t.Errorf("expected no article numbers, got %v", p.ArticleNumbers)
	}
}

func TestBuildPayloadTooLarge(t *testing.T) {
	r := fixtureResult()
	r.Regulation.Articles[0].Text = strings.Repeat("a", maxPayloadBytes+1)
	if _, err := BuildPointPayload(r); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected a size-guard error, got %v", err)
	}
}

func TestUserMessageEmbedsPayload(t *testing.T) {
	p, err := BuildPointPayload(fixtureResult())
	if err != nil {
		t.Fatal(err)
	}
	msg, err := userMessage(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"kind":"point"`, "Tomar", `"data_gaps"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("user message missing %q", want)
		}
	}
}

// The system prompt is the grounding contract — pin its load-bearing clauses.
func TestSystemPromptGrounding(t *testing.T) {
	for _, want := range []string{
		"pt-PT",
		"NEVER invent",
		"data_gaps",
		"indeterminado",
		"grau_incerteza",
		"leitura_estrategica",
		"not a legal opinion",
	} {
		if !strings.Contains(systemPrompt, want) {
			t.Errorf("system prompt missing %q", want)
		}
	}
}
