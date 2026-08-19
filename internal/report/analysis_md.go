package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/bernardosimoes/pdm/internal/ai"
	"github.com/bernardosimoes/pdm/internal/model"
)

// AnalysisMarkdown writes the AI analysis as a Markdown report, mirroring the
// HTML section order.
func AnalysisMarkdown(w io.Writer, v AnalysisView, a *ai.Analysis, gaps []string, tierLabel string) error {
	b := &strings.Builder{}
	title := a.Title
	if title == "" {
		title = "Análise urbanística — " + v.Municipality
	}
	fmt.Fprintf(b, "# %s\n\n", title)
	fmt.Fprintf(b, "_%s · confiança: %s_\n\n", tierLabel, confLabelPt(v.Confidence))
	fmt.Fprintf(b, "**%s** — %s\n", viabilityLabel(a.ViabilitySignal), a.ExecutiveSummary)

	fmt.Fprintf(b, "\n## Localização\n\n")
	fmt.Fprintf(b, "- **Município:** %s\n", v.Municipality)
	if v.Freguesia != "" {
		fmt.Fprintf(b, "- **Freguesia:** %s\n", v.Freguesia)
	}
	if v.Coord != nil {
		fmt.Fprintf(b, "- **Coordenadas (WGS84):** %.6f, %.6f\n", v.Coord.Lat, v.Coord.Lon)
	}
	if v.IsPolygon {
		fmt.Fprintf(b, "- **Área analisada:** %s m²\n", area(v.AreaM2))
	}
	if v.Plan != nil {
		fmt.Fprintf(b, "- **Instrumento de gestão territorial:** %s\n", v.Plan.Name)
		if v.Plan.PublishedRef != "" {
			fmt.Fprintf(b, "- **Publicação:** %s\n", v.Plan.PublishedRef)
		}
	}

	fmt.Fprintf(b, "\n## Síntese\n")
	for _, s := range a.Sections {
		fmt.Fprintf(b, "\n### %s\n\n%s\n", s.Heading, s.Reading)
		if s.Notes != "" {
			fmt.Fprintf(b, "\n> %s\n", s.Notes)
		}
	}

	fmt.Fprintf(b, "\n## Classificação do solo\n\n")
	if len(v.Zoning) == 0 {
		fmt.Fprintf(b, "- (sem categoria correspondente)\n")
	} else if v.IsPolygon {
		fmt.Fprintf(b, "| Área (m²) | %% | Categoria |\n|---:|---:|---|\n")
		for _, z := range v.Zoning {
			fmt.Fprintf(b, "| %s | %s%% | %s |\n", area(z.AreaM2), pct(z.Percent), z.Label)
		}
	} else {
		for _, z := range v.Zoning {
			fmt.Fprintf(b, "- %s\n", z.Label)
		}
	}

	fmt.Fprintf(b, "\n## Condicionantes\n\n")
	if len(v.Constraints) == 0 {
		fmt.Fprintf(b, "- (não avaliadas para este município)\n")
	}
	for _, c := range v.Constraints {
		// Unknown means the source had no data — a gap, never a "não" (the
		// same semantics every other renderer honours).
		state := "não"
		switch {
		case c.Unknown:
			state = "desconhecido — sem dados na fonte"
		case c.Present && c.AreaM2 == 0:
			state = "sim (aprox.)"
		case c.Present:
			state = "sim"
		}
		line := fmt.Sprintf("- **%s:** %s", c.Type, state)
		if v.IsPolygon && c.Present && c.AreaM2 > 0 {
			line += fmt.Sprintf(" — %s m² / %s%%", area(c.AreaM2), pct(c.Percent))
		} else if c.Detail != "" {
			line += " — " + c.Detail
		}
		fmt.Fprintf(b, "%s\n", line)
	}

	if len(v.Instruments) > 0 {
		fmt.Fprintf(b, "\n## Planos e programas especiais\n\n")
		for _, ins := range v.Instruments {
			line := fmt.Sprintf("- **%s** (%s)", ins.Name, stateLabel(ins.State))
			if ins.Diploma != "" {
				line += " — " + ins.Diploma
			}
			line += " — " + instrumentEvidencePt(ins)
			fmt.Fprintf(b, "%s\n", line)
		}
	}

	if len(a.Citations) > 0 {
		fmt.Fprintf(b, "\n## Artigos citados na análise\n\n")
		for _, c := range a.Citations {
			fmt.Fprintf(b, "- **Art. %s** — %s\n", c.ArticleNumber, c.Relevance)
		}
	}

	mdRegulationPt(b, v.Regulation)
	mdSourcesPt(b, v.Sources)
	mdDocumentsPt(b, v.Plan)

	if items := dedupe(gaps, a.Uncertainties, v.Notes); len(items) > 0 {
		fmt.Fprintf(b, "\n## Grau de incerteza e limitações\n\n")
		for _, n := range items {
			fmt.Fprintf(b, "- %s\n", n)
		}
	}

	fmt.Fprintf(b, "\n---\n\n> ⚠ %s\n", aiDisclaimer)
	mdDisclaimer(b, v.Disclaimer)
	_, err := io.WriteString(w, b.String())
	return err
}

// ---- pt-PT variants of the shared Markdown blocks ----

func mdRegulationPt(b *strings.Builder, r *model.Regulation) {
	if r == nil {
		return
	}
	fmt.Fprintf(b, "\n## Regulamento aplicável\n\n")
	fmt.Fprintf(b, "_Artigos correspondentes à categoria de uso do solo — leia-os na íntegra. Recolha automática, não constitui interpretação nem aconselhamento jurídico._\n\n")
	if r.Reference != "" {
		if r.URL != "" {
			fmt.Fprintf(b, "Fonte: [%s](%s)\n", r.Reference, r.URL)
		} else {
			fmt.Fprintf(b, "Fonte: %s\n", r.Reference)
		}
	}
	if len(r.Articles) == 0 {
		fmt.Fprintf(b, "\n- (nenhum artigo específico correspondeu; consulte o regulamento integral)\n")
		return
	}
	for _, a := range r.Articles {
		fmt.Fprintf(b, "\n### Artigo %s — %s\n", a.Number, a.Title)
		if a.Section != "" {
			fmt.Fprintf(b, "_Secção: %s_\n\n", a.Section)
		}
		if a.Text != "" {
			fmt.Fprintf(b, "%s\n", a.Text)
		}
	}
}

func mdSourcesPt(b *strings.Builder, sources []model.Source) {
	if len(sources) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## Fontes de dados\n\n")
	for _, s := range sources {
		line := fmt.Sprintf("- %s _(%s)_", s.Name, s.Provenance)
		if s.URL != "" {
			line = fmt.Sprintf("- [%s](%s) _(%s)_", s.Name, s.URL, s.Provenance)
		}
		if s.RetrievedAt != nil {
			line += " — obtido em " + s.RetrievedAt.Format("2006-01-02 15:04")
		}
		fmt.Fprintf(b, "%s\n", line)
	}
}

func mdDocumentsPt(b *strings.Builder, plan *model.PlanInfo) {
	if plan == nil || len(plan.Documents) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## Legislação e documentos\n\n")
	for _, d := range plan.Documents {
		if d.URL != "" {
			fmt.Fprintf(b, "- [%s](%s)", d.Title, d.URL)
		} else {
			fmt.Fprintf(b, "- %s", d.Title)
		}
		if d.Note != "" {
			fmt.Fprintf(b, " — %s", d.Note)
		}
		fmt.Fprintf(b, "\n")
	}
}
