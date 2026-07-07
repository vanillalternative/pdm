package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/bernardosimoes/pdm/internal/ai"
	"github.com/bernardosimoes/pdm/internal/model"
)

// aiDisclaimer is appended to every AI-generated analysis, in addition to the
// standard data disclaimer.
const aiDisclaimer = "O texto analítico deste relatório foi gerado por um modelo de IA exclusivamente a partir dos dados aqui listados e pode conter imprecisões. Não constitui parecer jurídico nem confere direitos."

// AnalysisView normalizes point and polygon results into the single shape the
// analysis renderers consume. Deterministic facts (coordinates, areas,
// municipality) always come from here, never from AI text.
type AnalysisView struct {
	Municipality string
	Freguesia    string
	Plan         *model.PlanInfo
	Coord        *model.Coordinate // point queries only
	AreaM2       float64           // polygon queries only
	IsPolygon    bool
	Zoning       []model.ZoningHit
	Constraints  []model.ConstraintHit
	Regulation   *model.Regulation
	Sources      []model.Source
	Confidence   model.Confidence
	Notes        []string
	Disclaimer   string
}

// NewPointAnalysisView adapts a point result.
func NewPointAnalysisView(r *model.PointResult) AnalysisView {
	c := r.Input
	return AnalysisView{
		Municipality: r.Municipality,
		Freguesia:    r.Freguesia,
		Plan:         r.Plan,
		Coord:        &c,
		Zoning:       r.Zoning,
		Constraints:  r.Constraints,
		Regulation:   r.Regulation,
		Sources:      r.Sources,
		Confidence:   r.Confidence,
		Notes:        r.Notes,
		Disclaimer:   r.Disclaimer,
	}
}

// NewPolygonAnalysisView adapts a polygon result.
func NewPolygonAnalysisView(r *model.PolygonResult) AnalysisView {
	return AnalysisView{
		Municipality: r.Municipality,
		Freguesia:    r.Freguesia,
		Plan:         r.Plan,
		AreaM2:       r.AnalysedAreaM2,
		IsPolygon:    true,
		Zoning:       r.Zoning,
		Constraints:  r.Constraints,
		Regulation:   r.Regulation,
		Sources:      r.Sources,
		Confidence:   r.Confidence,
		Notes:        r.Notes,
		Disclaimer:   r.Disclaimer,
	}
}

// AnalysisHTML writes a complete, self-contained HTML analysis report. gaps is
// the deterministic data-gap list from the AI payload; it is rendered even if
// the model ignored it. tierLabel identifies the tier/model used.
func AnalysisHTML(w io.Writer, v AnalysisView, a *ai.Analysis, gaps []string, mapFigure, tierLabel string) error {
	b := &strings.Builder{}
	title := a.Title
	if title == "" {
		title = "Análise urbanística — " + v.Municipality
	}
	head(b, title)

	fmt.Fprintf(b, `<div class="eyebrow">%s%s<span class="conf ai-badge">%s</span></div>`,
		planEyebrow(v.Plan), confidenceChipPt(v.Confidence), h(tierLabel))
	fmt.Fprintf(b, `<h1>%s</h1>`, h(title))
	fmt.Fprintf(b, `<p class="lede">Município de <strong>%s</strong>%s.</p>`, h(v.Municipality), planLedePt(v.Plan))

	// Viability banner + executive summary.
	fmt.Fprintf(b, `<div class="viability v-%s"><span class="v-tag">%s</span><p>%s</p></div>`,
		h(a.ViabilitySignal), h(viabilityLabel(a.ViabilitySignal)), h(a.ExecutiveSummary))

	if mapFigure != "" {
		b.WriteString(mapFigure)
	}

	// Localização — deterministic facts only.
	b.WriteString(`<section><h2>Localização</h2><table class="loc"><tbody>`)
	locRow(b, "Município", v.Municipality)
	if v.Freguesia != "" {
		locRow(b, "Freguesia", v.Freguesia)
	}
	if v.Coord != nil {
		locRow(b, "Coordenadas (WGS84)", fmt.Sprintf("%.6f, %.6f", v.Coord.Lat, v.Coord.Lon))
	}
	if v.IsPolygon {
		locRow(b, "Área analisada", area(v.AreaM2)+" m²")
	}
	if v.Plan != nil {
		locRow(b, "Instrumento de gestão territorial", v.Plan.Name)
		if v.Plan.PublishedRef != "" {
			locRow(b, "Publicação", v.Plan.PublishedRef)
		}
	}
	locRow(b, "Confiança dos dados", confLabelPt(v.Confidence))
	b.WriteString(`</tbody></table></section>`)

	// Síntese — the AI-written themed sections, canonical order.
	b.WriteString(`<section><h2>Síntese</h2>`)
	for _, s := range a.Sections {
		fmt.Fprintf(b, `<div class="ai-sec"><h3>%s</h3>`, h(s.Heading))
		for _, para := range paragraphs(s.Reading) {
			fmt.Fprintf(b, `<p>%s</p>`, h(para))
		}
		if s.Notes != "" {
			fmt.Fprintf(b, `<p class="ai-notes">%s</p>`, h(s.Notes))
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`</section>`)

	// Zoning + constraints — same deterministic markup as the plain reports.
	b.WriteString(`<section><h2>Classificação do solo</h2>`)
	if len(v.Zoning) == 0 {
		b.WriteString(`<p class="muted">Sem categoria de uso do solo correspondente.</p>`)
	} else if v.IsPolygon {
		b.WriteString(`<table><thead><tr><th>Categoria</th><th class="num">Área</th><th class="num">%</th></tr></thead><tbody>`)
		for _, z := range v.Zoning {
			fmt.Fprintf(b, `<tr><td>%s</td><td class="num">%s m²</td><td class="num">%s%%</td></tr>`, h(z.Label), area(z.AreaM2), pct(z.Percent))
		}
		b.WriteString(`</tbody></table>`)
	} else {
		for _, z := range v.Zoning {
			fmt.Fprintf(b, `<div class="zrow"><span class="ztag">%s</span><span class="zsub">%s</span></div>`, h(zHead(z)), h(z.Subclass))
		}
	}
	b.WriteString(`</section>`)

	b.WriteString(`<section><h2>Condicionantes</h2><div class="chips">`)
	if len(v.Constraints) == 0 {
		b.WriteString(`<p class="muted">Não avaliadas para este município — ver limitações abaixo.</p>`)
	}
	for _, c := range v.Constraints {
		if v.IsPolygon {
			extra := ""
			if c.Present {
				extra = fmt.Sprintf("%s m² · %s%%", area(c.AreaM2), pct(c.Percent))
			}
			chip(b, c.Type, c.Present, "", extra)
		} else {
			chip(b, c.Type, c.Present, c.Detail, "")
		}
	}
	b.WriteString(`</div></section>`)

	// Citations: each links to the verbatim article below.
	if len(a.Citations) > 0 {
		b.WriteString(`<section><h2>Artigos citados na análise</h2><ul class="cites">`)
		for _, c := range a.Citations {
			fmt.Fprintf(b, `<li><a href="#%s">Art. %s</a> — %s</li>`, h(artID(c.ArticleNumber)), h(c.ArticleNumber), h(c.Relevance))
		}
		b.WriteString(`</ul></section>`)
	}

	regulationHTMLPt(b, v.Regulation)

	// Legislação consultada: sources + plan documents.
	sourcesHTMLPt(b, v.Sources)
	documentsHTML(b, v.Plan)

	// Uncertainty panel: deterministic gaps ∪ AI uncertainties ∪ result notes.
	uncertaintyHTML(b, gaps, a.Uncertainties, v.Notes)

	fmt.Fprintf(b, `<footer><p>%s</p><p>%s</p></footer>`, h(aiDisclaimer), h(v.Disclaimer))
	return tail(w, b)
}

// ---- pt-PT variants of the shared blocks (the analysis report is pt-PT) ----

func planLedePt(p *model.PlanInfo) string {
	if p == nil || p.Name == "" {
		return ""
	}
	return ", ao abrigo do " + h(p.Name)
}

func confLabelPt(c model.Confidence) string {
	switch c {
	case model.ConfidenceHigh:
		return "alta"
	case model.ConfidenceMedium:
		return "média"
	case model.ConfidenceLow:
		return "baixa"
	}
	return string(c)
}

func confidenceChipPt(c model.Confidence) string {
	return fmt.Sprintf(`<span class="conf conf-%s">confiança: %s</span>`, c, confLabelPt(c))
}

func regulationHTMLPt(b *strings.Builder, reg *model.Regulation) {
	if reg == nil {
		return
	}
	b.WriteString(`<section><h2>Regulamento aplicável</h2>`)
	b.WriteString(`<p class="muted">Artigos correspondentes à categoria de uso do solo — leia-os na íntegra. Recolha automática, não constitui interpretação nem aconselhamento jurídico.</p>`)
	if reg.Reference != "" {
		if reg.URL != "" {
			fmt.Fprintf(b, `<p class="src-line">Fonte: <a href="%s">%s</a></p>`, h(reg.URL), h(reg.Reference))
		} else {
			fmt.Fprintf(b, `<p class="src-line">Fonte: %s</p>`, h(reg.Reference))
		}
	}
	if len(reg.Articles) == 0 {
		b.WriteString(`<p class="muted">Nenhum artigo específico correspondeu; consulte o regulamento integral.</p>`)
	}
	for _, a := range reg.Articles {
		fmt.Fprintf(b, `<details class="art" id="%s"><summary><span class="art-n">Art. %s</span> %s`, h(artID(a.Number)), h(a.Number), h(a.Title))
		if a.Section != "" {
			fmt.Fprintf(b, ` <span class="art-s">%s</span>`, h(a.Section))
		}
		fmt.Fprintf(b, `</summary><div class="art-text">%s</div></details>`, h(a.Text))
	}
	b.WriteString(`</section>`)
}

func sourcesHTMLPt(b *strings.Builder, sources []model.Source) {
	if len(sources) == 0 {
		return
	}
	b.WriteString(`<section><h2>Fontes de dados</h2><ul class="srcs">`)
	for _, s := range sources {
		line := h(s.Name)
		if s.URL != "" {
			line = fmt.Sprintf(`<a href="%s">%s</a>`, h(s.URL), h(s.Name))
		}
		fmt.Fprintf(b, `<li>%s <span class="prov">%s</span></li>`, line, h(string(s.Provenance)))
	}
	b.WriteString(`</ul></section>`)
}

func locRow(b *strings.Builder, k, v string) {
	fmt.Fprintf(b, `<tr><th scope="row">%s</th><td>%s</td></tr>`, h(k), h(v))
}

func documentsHTML(b *strings.Builder, plan *model.PlanInfo) {
	if plan == nil || len(plan.Documents) == 0 {
		return
	}
	b.WriteString(`<section><h2>Legislação e documentos</h2><ul class="srcs">`)
	for _, d := range plan.Documents {
		line := h(d.Title)
		if d.URL != "" {
			line = fmt.Sprintf(`<a href="%s">%s</a>`, h(d.URL), h(d.Title))
		}
		if d.Note != "" {
			line += fmt.Sprintf(` <span class="prov">%s</span>`, h(d.Note))
		}
		fmt.Fprintf(b, `<li>%s</li>`, line)
	}
	b.WriteString(`</ul></section>`)
}

func uncertaintyHTML(b *strings.Builder, groups ...[]string) {
	items := dedupe(groups...)
	if len(items) == 0 {
		return
	}
	b.WriteString(`<section class="notes"><h2>Grau de incerteza e limitações</h2><ul>`)
	for _, n := range items {
		fmt.Fprintf(b, `<li>%s</li>`, h(n))
	}
	b.WriteString(`</ul></section>`)
}

// dedupe flattens groups preserving order, dropping exact duplicates.
func dedupe(groups ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, g := range groups {
		for _, s := range g {
			if s == "" || seen[s] {
				continue
			}
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// paragraphs splits AI prose on blank lines.
func paragraphs(s string) []string {
	var out []string
	for _, p := range strings.Split(s, "\n\n") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func viabilityLabel(signal string) string {
	switch signal {
	case ai.ViabilityFavoravel:
		return "Favorável"
	case ai.ViabilityCondicionado:
		return "Condicionado"
	case ai.ViabilityDesfavoravel:
		return "Desfavorável"
	default:
		return "Indeterminado"
	}
}
