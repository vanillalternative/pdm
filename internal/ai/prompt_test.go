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
		Constraints:  []model.ConstraintHit{{Type: "REN", Label: "Reserva Ecológica Nacional", Present: true, Layer: "ren"}},
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
	// Constraints and regulation are present, so only the always-on gap remains.
	if len(p.DataGaps) != 1 || !strings.Contains(p.DataGaps[0], "acessos") {
		t.Errorf("unexpected data gaps: %v", p.DataGaps)
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
