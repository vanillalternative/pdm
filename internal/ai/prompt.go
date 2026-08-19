package ai

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bernardosimoes/pdm/internal/model"
)

// maxPayloadBytes guards against pathological payloads before they hit the
// API as a confusing request-too-large error.
const maxPayloadBytes = 400 << 10

// Payload is what the model receives: the raw query result plus the data gaps
// computed deterministically in Go (never left for the model to guess).
type Payload struct {
	Kind     string          `json:"kind"` // "point" | "polygon"
	Result   json.RawMessage `json:"result"`
	DataGaps []string        `json:"data_gaps"`

	// ArticleNumbers lists the regulation articles present in Result, used to
	// validate citations in the response. Not sent to the model as a separate
	// field (the articles are already in Result).
	ArticleNumbers []string `json:"-"`
}

// BuildPointPayload prepares the model payload for a point result.
func BuildPointPayload(r *model.PointResult) (Payload, error) {
	return buildPayload("point", r, r.Constraints, r.Regulation)
}

// BuildPolygonPayload prepares the model payload for a polygon result.
func BuildPolygonPayload(r *model.PolygonResult) (Payload, error) {
	return buildPayload("polygon", r, r.Constraints, r.Regulation)
}

func buildPayload(kind string, result any, constraints []model.ConstraintHit, reg *model.Regulation) (Payload, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return Payload{}, fmt.Errorf("marshal query result: %w", err)
	}
	if len(raw) > maxPayloadBytes {
		return Payload{}, fmt.Errorf("query result too large for analysis (%d KB, limit %d KB)", len(raw)>>10, maxPayloadBytes>>10)
	}
	var nums []string
	if reg != nil {
		for _, a := range reg.Articles {
			nums = append(nums, a.Number)
		}
	}
	return Payload{
		Kind:           kind,
		Result:         raw,
		DataGaps:       dataGaps(constraints, reg),
		ArticleNumbers: nums,
	}, nil
}

// dataGaps enumerates what this analysis does NOT know, deterministically.
// The list is sent to the model (which must acknowledge the gaps) and is also
// rendered in the report independently of the model complying. An evaluated
// constraint that is absent is NOT a gap — only a constraint type that was
// never checked is.
func dataGaps(constraints []model.ConstraintHit, reg *model.Regulation) []string {
	// Only hits that actually got an answer count as evaluated: an Unknown hit
	// means the source was asked and could not answer for this municipality —
	// that is a gap, not an evaluation.
	evaluated := make(map[string]bool, len(constraints))
	var gaps []string
	for _, c := range constraints {
		if c.Unknown {
			gaps = append(gaps, c.Type+": sem dados na fonte nacional para este município — não avaliado.")
			continue
		}
		evaluated[c.Type] = true
	}
	switch {
	case len(constraints) == 0:
		gaps = append(gaps, "Condicionantes legais (RAN, REN, Rede Natura 2000, perigosidade de incêndio, servidões administrativas) não avaliadas — a análise baseia-se apenas na classificação de uso do solo (CRUS).")
	default:
		if !evaluated["RAN"] || !evaluated["REN"] {
			gaps = append(gaps, "Condicionantes municipais (RAN, REN, servidões locais do PDM) não avaliadas para este município — apenas as condicionantes de âmbito nacional foram verificadas.")
		}
		if !hasTypeContaining(evaluated, "Natura 2000") || !hasTypeContaining(evaluated, "Perigosidade") {
			gaps = append(gaps, "Condicionantes nacionais (Rede Natura 2000, perigosidade de incêndio rural) não verificadas nesta análise.")
		}
	}
	if reg == nil || len(reg.Articles) == 0 {
		gaps = append(gaps, "Regulamento do plano municipal não disponível nesta análise — os parâmetros urbanísticos concretos (índices, cérceas, usos admitidos) não foram verificados.")
	}
	gaps = append(gaps, "Sem dados sobre acessos e infraestruturas, morfologia e topografia do terreno, cadastro predial ou imagem de satélite.")
	return gaps
}

// hasTypeContaining reports whether any evaluated constraint type contains
// sub. Substring (not prefix) matching, because the canonical type strings
// vary in shape: "Rede Natura 2000 (ZEC)" from the national probes and srup
// geometry layers vs adapter-specific variants.
func hasTypeContaining(evaluated map[string]bool, sub string) bool {
	for t := range evaluated {
		if strings.Contains(t, sub) {
			return true
		}
	}
	return false
}

// systemPrompt defines the analyst role and the grounding rules. Instructions
// are in English (followed equally well, cheaper in tokens); the output
// language is pinned to European Portuguese.
const systemPrompt = `You are a senior Portuguese urban-planning analyst (urbanista). You write per-parcel planning analyses grounded in municipal master plans (PDM) and other territorial management instruments (IGT). You will receive a JSON payload with the result of a spatial planning query for one location or parcel in Portugal: municipality, plan in force, zoning classification, legal constraints, verbatim regulation articles, data sources, confidence level, and an explicit list of data gaps.

Write the analysis in formal European Portuguese (pt-PT, never pt-BR). Output must match the required JSON schema.

HARD GROUNDING RULES — violating any of these makes the report worthless:
1. Use ONLY facts present in the payload. NEVER invent areas, distances, road access, infrastructure, topography, slope, cadastral status, urban indices (índice de utilização, índice de implantação, cércea), or the contents of regulation articles.
2. The payload's "data_gaps" list what is unknown. For themes without data (acessos_infraestruturas, morfologia_topografia, and the numeric side of potencial_construtivo), still write the section, but state explicitly that the information is not available in this analysis ("informação não disponível nesta análise") and limit yourself to what can be reasoned from the zoning class and constraints, clearly flagged as inference ("com base apenas em...").
3. Cite regulation only by article numbers that appear in the payload's regulation articles, and only for what those articles actually say. Quote at most short fragments. Every citation goes in "citations" with the exact article number.
4. Calibrate your language to the payload's "confidence" and "data_gaps". The "grau_incerteza" section must enumerate the data gaps and their practical consequences.
5. "viability_signal" must be "indeterminado" whenever confidence is low or legal constraints were not evaluated. Use "favoravel"/"condicionado"/"desfavoravel" only when the payload actually supports that reading, and justify it in "executive_summary".
6. Produce ALL of these sections, exactly once each, in this order: identificacao, enquadramento_urbanistico, condicionantes_servidoes, acessos_infraestruturas, morfologia_topografia, potencial_construtivo, riscos_principais, grau_incerteza, leitura_estrategica. Section "heading" is a short pt-PT display title; "reading" is the analytical prose ("Leitura"); "notes" carries legal/procedural observations ("Notas / Observações") or is an empty string.
7. "uncertainties" lists, in pt-PT, every material uncertainty of this analysis (include the data gaps and anything else you flagged).
8. This is decision-support material, not a legal opinion, and never confers rights. Do not promise outcomes; recommend confirmation with the competent Câmara Municipal where appropriate.`

// userMessage renders the payload into the user turn.
func userMessage(p Payload) (string, error) {
	body, err := json.Marshal(struct {
		Kind     string          `json:"kind"`
		Result   json.RawMessage `json:"result"`
		DataGaps []string        `json:"data_gaps"`
	}{p.Kind, p.Result, p.DataGaps})
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	return "Produce the property planning analysis for this query result:\n\n```json\n" + string(body) + "\n```", nil
}
