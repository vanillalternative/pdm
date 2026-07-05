// Package report renders query results in the three output formats: a
// human-readable terminal view, machine-readable JSON, and a Markdown report.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/bernardosimoes/pdm/internal/model"
)

// Format is an output format selector.
type Format string

const (
	FormatText     Format = "text"
	FormatJSON     Format = "json"
	FormatMarkdown Format = "markdown"
)

// ParseFormat validates and normalizes a format string.
func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "text", "terminal", "human":
		return FormatText, nil
	case "json":
		return FormatJSON, nil
	case "markdown", "md":
		return FormatMarkdown, nil
	default:
		return "", fmt.Errorf("unknown format %q (use text, json, or markdown)", s)
	}
}

// JSON writes any value as indented JSON.
func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// ---- Point ----

// Point renders a point result in the given format.
func Point(w io.Writer, r *model.PointResult, f Format) error {
	switch f {
	case FormatJSON:
		return JSON(w, r)
	case FormatMarkdown:
		return pointMarkdown(w, r)
	default:
		return pointText(w, r)
	}
}

func pointText(w io.Writer, r *model.PointResult) error {
	b := &strings.Builder{}
	fmt.Fprintf(b, "Input:        %.5f, %.5f (lat, lon)\n", r.Input.Lat, r.Input.Lon)
	fmt.Fprintf(b, "Municipality: %s\n", r.Municipality)
	if !r.Supported {
		writeNotes(b, r.Notes)
		writeConfidence(b, r.Confidence)
		writeDisclaimer(b, r.Disclaimer)
		_, err := io.WriteString(w, b.String())
		return err
	}
	if r.Plan != nil {
		fmt.Fprintf(b, "Applicable plan: %s\n", r.Plan.Name)
	}
	fmt.Fprintf(b, "\nZoning:\n")
	if len(r.Zoning) == 0 {
		fmt.Fprintf(b, "  - (none matched)\n")
	}
	for _, z := range r.Zoning {
		fmt.Fprintf(b, "  - %s\n", z.Label)
	}
	fmt.Fprintf(b, "\nConstraints:\n")
	if len(r.Constraints) == 0 {
		fmt.Fprintf(b, "  - (none evaluated)\n")
	}
	for _, c := range r.Constraints {
		fmt.Fprintf(b, "  - %s\n", constraintPointLine(c))
	}
	writeRegulation(b, r.Regulation)
	writeSources(b, r.Sources)
	writeNotes(b, r.Notes)
	writeConfidence(b, r.Confidence)
	writeDisclaimer(b, r.Disclaimer)
	_, err := io.WriteString(w, b.String())
	return err
}

func constraintPointLine(c model.ConstraintHit) string {
	if c.Present {
		if c.Detail != "" {
			return fmt.Sprintf("%s: yes — %s", c.Type, c.Detail)
		}
		return fmt.Sprintf("%s: yes", c.Type)
	}
	if strings.Contains(strings.ToLower(c.Type), "servid") {
		return fmt.Sprintf("%s: none detected", c.Type)
	}
	return fmt.Sprintf("%s: no", c.Type)
}

func pointMarkdown(w io.Writer, r *model.PointResult) error {
	b := &strings.Builder{}
	fmt.Fprintf(b, "# Planning query — %.5f, %.5f\n\n", r.Input.Lat, r.Input.Lon)
	fmt.Fprintf(b, "- **Municipality:** %s\n", r.Municipality)
	if !r.Supported {
		mdNotes(b, r.Notes)
		fmt.Fprintf(b, "- **Confidence:** %s\n", r.Confidence)
		mdDisclaimer(b, r.Disclaimer)
		_, err := io.WriteString(w, b.String())
		return err
	}
	if r.Plan != nil {
		fmt.Fprintf(b, "- **Applicable plan:** %s\n", r.Plan.Name)
	}
	fmt.Fprintf(b, "- **Confidence:** %s\n", r.Confidence)
	fmt.Fprintf(b, "\n## Zoning\n\n")
	if len(r.Zoning) == 0 {
		fmt.Fprintf(b, "- (none matched)\n")
	}
	for _, z := range r.Zoning {
		fmt.Fprintf(b, "- %s\n", z.Label)
	}
	fmt.Fprintf(b, "\n## Constraints\n\n")
	if len(r.Constraints) == 0 {
		fmt.Fprintf(b, "- (none evaluated)\n")
	}
	for _, c := range r.Constraints {
		fmt.Fprintf(b, "- %s\n", constraintPointLine(c))
	}
	mdRegulation(b, r.Regulation)
	mdSources(b, r.Sources)
	mdDocuments(b, r.Plan)
	mdNotes(b, r.Notes)
	mdDisclaimer(b, r.Disclaimer)
	_, err := io.WriteString(w, b.String())
	return err
}

// ---- Polygon ----

// Polygon renders a polygon result in the given format.
func Polygon(w io.Writer, r *model.PolygonResult, f Format) error {
	switch f {
	case FormatJSON:
		return JSON(w, r)
	case FormatMarkdown:
		return polygonMarkdown(w, r)
	default:
		return polygonText(w, r)
	}
}

func polygonText(w io.Writer, r *model.PolygonResult) error {
	b := &strings.Builder{}
	fmt.Fprintf(b, "Municipality: %s\n", r.Municipality)
	if !r.Supported {
		fmt.Fprintf(b, "Analysed area: %s m²\n\n", area(r.AnalysedAreaM2))
		writeNotes(b, r.Notes)
		writeConfidence(b, r.Confidence)
		writeDisclaimer(b, r.Disclaimer)
		_, err := io.WriteString(w, b.String())
		return err
	}
	if r.Plan != nil {
		fmt.Fprintf(b, "Applicable plan: %s\n", r.Plan.Name)
	}
	fmt.Fprintf(b, "Analysed area: %s m²\n", area(r.AnalysedAreaM2))
	fmt.Fprintf(b, "\nZoning:\n")
	if len(r.Zoning) == 0 {
		fmt.Fprintf(b, "  - (none matched)\n")
	}
	for _, z := range r.Zoning {
		fmt.Fprintf(b, "  - %s m² / %s%% — %s\n", area(z.AreaM2), pct(z.Percent), z.Label)
	}
	fmt.Fprintf(b, "\nConstraints:\n")
	for _, c := range r.Constraints {
		fmt.Fprintf(b, "  - %s m² / %s%% — %s\n", area(c.AreaM2), pct(c.Percent), c.Type)
	}
	writeRegulation(b, r.Regulation)
	writeSources(b, r.Sources)
	writeNotes(b, r.Notes)
	writeConfidence(b, r.Confidence)
	writeDisclaimer(b, r.Disclaimer)
	_, err := io.WriteString(w, b.String())
	return err
}

func polygonMarkdown(w io.Writer, r *model.PolygonResult) error {
	b := &strings.Builder{}
	fmt.Fprintf(b, "# Planning query — parcel\n\n")
	fmt.Fprintf(b, "- **Municipality:** %s\n", r.Municipality)
	if r.Plan != nil {
		fmt.Fprintf(b, "- **Applicable plan:** %s\n", r.Plan.Name)
	}
	fmt.Fprintf(b, "- **Analysed area:** %s m²\n", area(r.AnalysedAreaM2))
	fmt.Fprintf(b, "- **Confidence:** %s\n", r.Confidence)
	if r.Supported {
		fmt.Fprintf(b, "\n## Zoning\n\n| Area (m²) | %% | Category |\n|---:|---:|---|\n")
		for _, z := range r.Zoning {
			fmt.Fprintf(b, "| %s | %s%% | %s |\n", area(z.AreaM2), pct(z.Percent), z.Label)
		}
		fmt.Fprintf(b, "\n## Constraints\n\n| Area (m²) | %% | Constraint |\n|---:|---:|---|\n")
		for _, c := range r.Constraints {
			fmt.Fprintf(b, "| %s | %s%% | %s |\n", area(c.AreaM2), pct(c.Percent), c.Type)
		}
	}
	mdRegulation(b, r.Regulation)
	mdSources(b, r.Sources)
	mdDocuments(b, r.Plan)
	mdNotes(b, r.Notes)
	mdDisclaimer(b, r.Disclaimer)
	_, err := io.WriteString(w, b.String())
	return err
}

// ---- shared helpers ----

func writeRegulation(b *strings.Builder, r *model.Regulation) {
	if r == nil {
		return
	}
	fmt.Fprintf(b, "\nApplicable regulation (read the full text — not interpreted here):\n")
	if r.Reference != "" || r.URL != "" {
		fmt.Fprintf(b, "  %s  %s\n", r.Reference, r.URL)
	}
	if len(r.Articles) == 0 {
		fmt.Fprintf(b, "  - (no section-specific article matched; consult the full regulation)\n")
		return
	}
	for _, a := range r.Articles {
		sec := ""
		if a.Section != "" {
			sec = "  [" + a.Section + "]"
		}
		fmt.Fprintf(b, "  - Art. %s — %s%s\n", a.Number, a.Title, sec)
	}
	fmt.Fprintf(b, "  (full article text: --format json or markdown)\n")
}

func mdRegulation(b *strings.Builder, r *model.Regulation) {
	if r == nil {
		return
	}
	fmt.Fprintf(b, "\n## Applicable regulation\n\n")
	fmt.Fprintf(b, "_Candidate articles matched to the zoning category — read them in full. Retrieval only, not an interpretation or legal advice._\n\n")
	if r.Reference != "" {
		if r.URL != "" {
			fmt.Fprintf(b, "Source: [%s](%s)\n", r.Reference, r.URL)
		} else {
			fmt.Fprintf(b, "Source: %s\n", r.Reference)
		}
	}
	if len(r.Articles) == 0 {
		fmt.Fprintf(b, "\n- (no section-specific article matched; consult the full regulation)\n")
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

func writeSources(b *strings.Builder, sources []model.Source) {
	fmt.Fprintf(b, "\nSources:\n")
	if len(sources) == 0 {
		fmt.Fprintf(b, "  - (none)\n")
	}
	for _, s := range sources {
		line := "  - " + s.Name
		line += " [" + string(s.Provenance) + "]"
		if s.RetrievedAt != nil {
			line += " retrieved " + s.RetrievedAt.Format("2006-01-02 15:04")
		}
		fmt.Fprintf(b, "%s\n", line)
	}
}

func writeNotes(b *strings.Builder, notes []string) {
	if len(notes) == 0 {
		return
	}
	fmt.Fprintf(b, "\nNotes:\n")
	for _, n := range notes {
		fmt.Fprintf(b, "  ! %s\n", n)
	}
}

func writeConfidence(b *strings.Builder, c model.Confidence) {
	fmt.Fprintf(b, "\nConfidence: %s\n", c)
}

func writeDisclaimer(b *strings.Builder, d string) {
	if d == "" {
		return
	}
	fmt.Fprintf(b, "\n⚠  %s\n", d)
}

func mdSources(b *strings.Builder, sources []model.Source) {
	fmt.Fprintf(b, "\n## Sources\n\n")
	for _, s := range sources {
		line := fmt.Sprintf("- %s _(%s)_", s.Name, s.Provenance)
		if s.URL != "" {
			line = fmt.Sprintf("- [%s](%s) _(%s)_", s.Name, s.URL, s.Provenance)
		}
		if s.RetrievedAt != nil {
			line += " — retrieved " + s.RetrievedAt.Format("2006-01-02 15:04")
		}
		fmt.Fprintf(b, "%s\n", line)
	}
}

func mdDocuments(b *strings.Builder, plan *model.PlanInfo) {
	if plan == nil || len(plan.Documents) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## Regulations & documents\n\n")
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

func mdNotes(b *strings.Builder, notes []string) {
	if len(notes) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## Notes\n\n")
	for _, n := range notes {
		fmt.Fprintf(b, "- %s\n", n)
	}
}

func mdDisclaimer(b *strings.Builder, d string) {
	if d == "" {
		return
	}
	fmt.Fprintf(b, "\n---\n\n> ⚠ %s\n", d)
}

// area rounds to whole m² and groups thousands with commas.
func area(m2 float64) string {
	if math.IsNaN(m2) || math.IsInf(m2, 0) {
		return "0"
	}
	return group(int64(math.Round(m2)))
}

// pct formats a percentage to one decimal place.
func pct(p float64) string {
	if math.IsNaN(p) || math.IsInf(p, 0) {
		return "0.0"
	}
	return strconv.FormatFloat(p, 'f', 1, 64)
}

// group inserts comma thousands separators into an integer.
func group(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.FormatInt(n, 10)
	if len(s) <= 3 {
		if neg {
			return "-" + s
		}
		return s
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	out := strings.Join(parts, ",")
	if neg {
		return "-" + out
	}
	return out
}
