// Package ai turns a planning query result into a written property analysis:
// it sends the structured result to a Claude model and receives structured
// pt-PT report sections back. The model writes prose only — every fact it may
// rely on is contained in the payload it receives, and its output is
// post-validated in Go before rendering.
package ai

import (
	"context"
	"fmt"
)

// Tier selects the model behind an analysis, mapping to a pricing tier.
type Tier string

const (
	// TierBasic uses a fast, low-cost model.
	TierBasic Tier = "basic"
	// TierPremium uses the most capable model, with adaptive thinking.
	TierPremium Tier = "premium"
)

// ParseTier parses a user-supplied tier name.
func ParseTier(s string) (Tier, error) {
	switch Tier(s) {
	case TierBasic, TierPremium:
		return Tier(s), nil
	default:
		return "", fmt.Errorf("unknown tier %q (expected basic or premium)", s)
	}
}

// Generator produces an analysis from a payload. The CLI depends on this seam
// so tests can substitute a stub for the real API client.
type Generator interface {
	Generate(ctx context.Context, p Payload) (*Analysis, error)
}

// Viability signals for the report banner.
const (
	ViabilityFavoravel     = "favoravel"
	ViabilityCondicionado  = "condicionado"
	ViabilityDesfavoravel  = "desfavoravel"
	ViabilityIndeterminado = "indeterminado"
)

// SectionIDs is the canonical, ordered list of analysis themes. It mirrors the
// structure of professional per-parcel planning reports (síntese themes).
var SectionIDs = []string{
	"identificacao",
	"enquadramento_urbanistico",
	"condicionantes_servidoes",
	"acessos_infraestruturas",
	"morfologia_topografia",
	"potencial_construtivo",
	"riscos_principais",
	"grau_incerteza",
	"leitura_estrategica",
}

// Analysis is the structured output the model must produce (all prose pt-PT).
type Analysis struct {
	Title            string     `json:"title"`
	ExecutiveSummary string     `json:"executive_summary"`
	ViabilitySignal  string     `json:"viability_signal"`
	Sections         []Section  `json:"sections"`
	Citations        []Citation `json:"citations"`
	Uncertainties    []string   `json:"uncertainties"`
}

// Section is one themed block of the síntese.
type Section struct {
	ID      string `json:"id"`
	Heading string `json:"heading"`
	Reading string `json:"reading"` // "Leitura" prose
	Notes   string `json:"notes"`   // "Notas / Observações" (may be empty)
}

// Citation points at a regulation article that grounds the analysis. Article
// numbers are validated against the payload; unknown ones are dropped.
type Citation struct {
	ArticleNumber string `json:"article_number"`
	Relevance     string `json:"relevance"`
}
