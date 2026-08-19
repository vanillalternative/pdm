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
	FormatHTML     Format = "html"
	// FormatNDJSON streams newline-delimited JSON events as the query runs:
	// meta first, one event per layer as it completes, then the full result.
	FormatNDJSON Format = "ndjson"
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
	case "html":
		return FormatHTML, nil
	case "ndjson", "stream":
		return FormatNDJSON, nil
	default:
		return "", fmt.Errorf("unknown format %q (use text, json, markdown, html, or ndjson)", s)
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
	writeIntro(b, pointIntro(r))
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
		if c.Note != "" {
			fmt.Fprintf(b, "    (%s)\n", c.Note)
		}
		writeConstraintContext(b, c)
	}
	writeInstruments(b, r.Instruments)
	writeRegulation(b, r.Regulation)
	writeSources(b, r.Sources)
	writeNotes(b, r.Notes)
	writeConfidence(b, r.Confidence)
	writeDisclaimer(b, r.Disclaimer)
	_, err := io.WriteString(w, b.String())
	return err
}

func constraintPointLine(c model.ConstraintHit) string {
	if c.Unknown {
		if c.Detail != "" {
			return fmt.Sprintf("%s: unknown — %s", c.Type, c.Detail)
		}
		return fmt.Sprintf("%s: unknown (no data for this municipality)", c.Type)
	}
	if c.Present {
		if c.Detail != "" {
			return fmt.Sprintf("%s: yes — %s", c.Type, c.Detail)
		}
		return fmt.Sprintf("%s: yes", c.Type)
	}
	if c.Detail != "" { // a "no" that carries an explanation (e.g. REN exclusion area)
		return fmt.Sprintf("%s: no — %s", c.Type, c.Detail)
	}
	if strings.Contains(strings.ToLower(c.Type), "servid") {
		return fmt.Sprintf("%s: none detected", c.Type)
	}
	return fmt.Sprintf("%s: no", c.Type)
}

func constraintPresence(c model.ConstraintHit) string {
	switch {
	case c.Unknown:
		return "unknown"
	case c.AreaM2 > 0:
		return "yes"
	case c.Present && isEnvelopeApprox(c):
		return "yes (approx.)"
	case c.Present:
		return "yes"
	default:
		return "no"
	}
}

func constraintEvidence(c model.ConstraintHit) string {
	switch {
	case c.Unknown:
		return "Source unavailable or incomplete for this municipality."
	case c.AreaM2 > 0:
		return fmt.Sprintf("Calculated overlap: %s m² / %s%% of the analysed area.", area(c.AreaM2), pct(c.Percent))
	case c.Present && isEnvelopeApprox(c):
		return "The parcel envelope intersects this layer; exact affected area and percentage were not calculated."
	case c.Present:
		return "The queried location intersects this layer."
	default:
		return "No intersection detected in the evaluated source layer."
	}
}

func constraintMeaning(c model.ConstraintHit) string {
	t := strings.ToLower(c.Type + " " + c.Label)
	switch {
	case strings.Contains(t, "ren"):
		return "REN is the Reserva Ecológica Nacional: ecological and risk-prevention land constraints. If the affected area is really inside REN, construction, earthworks, impermeabilisation, changes of use and vegetation removal may be prohibited, restricted, or require prior compatibility checks."
	case strings.Contains(t, "ran"):
		return "RAN is the Reserva Agrícola Nacional: agricultural soil protection constraints. Non-agricultural uses may be restricted or require an exception/authorisation."
	case strings.Contains(t, "rede natura") || strings.Contains(t, "zec") || strings.Contains(t, "zpe"):
		return "Rede Natura 2000 marks EU nature-conservation areas; projects may need compatibility assessment even where the PDM allows a use."
	case strings.Contains(t, "área protegida") || strings.Contains(t, "area protegida"):
		return "Protected-area boundaries can trigger ICNF/special-plan rules in addition to municipal zoning."
	case strings.Contains(t, "orla costeira") || strings.Contains(t, "salvaguarda costeira"):
		return "Coastal-program layers can impose protection or safeguard rules in addition to municipal zoning."
	case strings.Contains(t, "albufeira") || strings.Contains(t, "poaap") || strings.Contains(t, "paap"):
		return "Reservoir-plan layers can impose water-resource protection rules and buffer constraints."
	default:
		return ""
	}
}

func constraintLimitation(c model.ConstraintHit) string {
	t := strings.ToLower(c.Type + " " + c.Label)
	switch {
	case c.Unknown:
		return "This result cannot distinguish absence of a constraint from absence of source data."
	case !c.Present:
		return ""
	case strings.Contains(t, "ren"):
		lim := "This report does not identify the REN subtype (for example flood, erosion, aquifer recharge or coastal-risk system)."
		if isEnvelopeApprox(c) {
			lim += " It also does not calculate the exact affected area."
		}
		return lim
	case isEnvelopeApprox(c):
		return "This was tested with the parcel envelope, so it is a presence warning rather than a measured overlap."
	default:
		return ""
	}
}

func constraintNextStep(c model.ConstraintHit) string {
	t := strings.ToLower(c.Type + " " + c.Label)
	switch {
	case !c.Present || c.Unknown:
		return ""
	case strings.Contains(t, "ren"):
		return "Obtain the municipal REN plant/extract for this parcel and confirm the REN subtype and exact overlap before assuming buildability."
	case strings.Contains(t, "ran"):
		return "Confirm the exact RAN overlap and whether the intended use has a legal exception or entity authorisation route."
	case strings.Contains(t, "rede natura") || strings.Contains(t, "zec") || strings.Contains(t, "zpe"):
		return "Confirm whether the intended operation requires an ICNF/Rede Natura compatibility assessment."
	case strings.Contains(t, "área protegida") || strings.Contains(t, "area protegida"):
		return "Consult the protected-area regulation and ICNF/municipal licensing requirements for the intended use."
	case strings.Contains(t, "orla costeira") || strings.Contains(t, "salvaguarda costeira"):
		return "Verify the coastal-program class/faixa and whether the use is restricted by APA or beach/coastal management rules."
	case strings.Contains(t, "albufeira") || strings.Contains(t, "poaap") || strings.Contains(t, "paap"):
		return "Verify the reservoir-program zone/buffer and any APA water-resource restrictions."
	default:
		return ""
	}
}

type reportIntro struct {
	Lead     string
	Bullets  []string
	Articles []string
}

func pointIntro(r *model.PointResult) reportIntro {
	subject := fmt.Sprintf("This report checks the planning rules at %.5f, %.5f in %s.", r.Input.Lat, r.Input.Lon, r.Municipality)
	return buildIntro(subject, 0, r.Zoning, r.Constraints, r.Instruments, r.Regulation)
}

func polygonIntro(r *model.PolygonResult) reportIntro {
	subject := fmt.Sprintf("This report checks the planning rules for the analysed parcel in %s.", r.Municipality)
	return buildIntro(subject, r.AnalysedAreaM2, r.Zoning, r.Constraints, r.Instruments, r.Regulation)
}

func buildIntro(subject string, analysedArea float64, zoning []model.ZoningHit, constraints []model.ConstraintHit, instruments []model.Instrument, reg *model.Regulation) reportIntro {
	intro := reportIntro{Lead: subject}
	if analysedArea > 0 {
		intro.Lead += fmt.Sprintf(" The analysed area is about %s m².", area(analysedArea))
	}

	if z := zoningIntro(zoning); z != "" {
		intro.Bullets = append(intro.Bullets, z)
	}
	intro.Bullets = append(intro.Bullets, constraintIntroBullets(constraints)...)
	intro.Bullets = append(intro.Bullets, instrumentIntroBullets(instruments)...)
	intro.Articles = applicableArticleExplanations(reg, analysedArea, zoning, constraints)
	return intro
}

func zoningIntro(zoning []model.ZoningHit) string {
	if len(zoning) == 0 {
		return "No zoning category was matched in the available planning layer, so the report cannot infer the base land-use class from the map data."
	}
	if len(zoning) == 1 {
		z := zoning[0]
		if z.AreaM2 > 0 {
			return fmt.Sprintf("The land is mapped mainly as %s (%s m², %s%%). This is the base PDM category used to decide what uses and building rules may be possible.", z.Label, area(z.AreaM2), pct(z.Percent))
		}
		return fmt.Sprintf("The location is mapped as %s. This is the base PDM category used to decide what uses and building rules may be possible.", z.Label)
	}
	parts := make([]string, 0, minInt(len(zoning), 3))
	for _, z := range zoning[:minInt(len(zoning), 3)] {
		if z.AreaM2 > 0 {
			parts = append(parts, fmt.Sprintf("%s (%s%%)", z.Label, pct(z.Percent)))
		} else {
			parts = append(parts, z.Label)
		}
	}
	return "The parcel crosses more than one zoning category: " + strings.Join(parts, "; ") + ". Each part may have different rules, so decisions should be checked against the affected area, not only the largest category."
}

func constraintIntroBullets(constraints []model.ConstraintHit) []string {
	var out []string
	for _, c := range constraints {
		if c.Unknown {
			out = append(out, fmt.Sprintf("%s could not be confirmed from the available source data. Treat this as a data gap, not as proof that the constraint is absent.", c.Type))
			continue
		}
		if !c.Present {
			continue
		}
		line := practicalConstraintMeaning(c)
		if c.AreaM2 > 0 {
			line += fmt.Sprintf(" The measured overlap is about %s m² (%s%% of the analysed area).", area(c.AreaM2), pct(c.Percent))
		} else if isEnvelopeApprox(c) {
			line += " The report only detected an approximate intersection with the parcel envelope, so the exact affected area still needs confirmation."
		}
		out = append(out, line)
	}
	if len(out) == 0 && len(constraints) > 0 {
		out = append(out, "No evaluated constraint layer intersected the location/parcel. This does not replace an official municipal extract, but it means the checked layers did not raise a positive flag.")
	}
	return out
}

func practicalConstraintMeaning(c model.ConstraintHit) string {
	t := strings.ToLower(c.Type + " " + c.Label + " " + c.Detail)
	switch {
	case strings.Contains(t, "ren"):
		return "REN means Reserva Ecológica Nacional. In practical terms, the affected land is treated as ecologically sensitive or risk-prone, so building, earthworks, soil sealing, vegetation removal or changes of use may be limited or may need a specific compatibility route."
	case strings.Contains(t, "ran"):
		return "RAN means Reserva Agrícola Nacional. In practical terms, the land is protected for agricultural value, so non-agricultural building or uses are usually restricted unless an exception or authorisation applies."
	case strings.Contains(t, "rede natura") || strings.Contains(t, "zec") || strings.Contains(t, "zpe"):
		return "Rede Natura 2000 means the land is within an EU nature-conservation area. In practical terms, even uses allowed by the PDM may need an environmental compatibility check."
	case strings.Contains(t, "albufeira") || strings.Contains(t, "poaap") || strings.Contains(t, "paap") || strings.Contains(t, "castelo de bode"):
		return "This reservoir-plan finding means the land may be subject to water-resource protection rules around the reservoir. In practical terms, uses, construction, wastewater, earthworks and vegetation changes may need extra checks with the special plan and water authority rules."
	case strings.Contains(t, "orla costeira") || strings.Contains(t, "salvaguarda costeira"):
		return "This coastal-program finding means coastal protection rules may add limits beyond the PDM. In practical terms, confirm the exact class/faixa before assuming construction or land-use changes are possible."
	case strings.Contains(t, "área protegida") || strings.Contains(t, "area protegida"):
		return "This protected-area finding means conservation rules may apply in addition to the PDM. In practical terms, the intended use may require ICNF or municipal checks before licensing."
	default:
		if c.Detail != "" {
			return fmt.Sprintf("%s was detected: %s. In practical terms, treat it as an extra licensing or restriction flag to verify before deciding what can be done.", c.Type, c.Detail)
		}
		return fmt.Sprintf("%s was detected. In practical terms, treat it as an extra licensing or restriction flag to verify before deciding what can be done.", c.Type)
	}
}

func instrumentIntroBullets(instruments []model.Instrument) []string {
	var out []string
	for _, i := range instruments {
		inside := i.PointInside != nil && *i.PointInside
		if !inside && i.State != "vigor" && i.State != "parcial" {
			continue
		}
		if !inside {
			out = append(out, fmt.Sprintf("%s is listed for the municipality, but this report did not confirm that the parcel/location is inside its boundary. Treat it as municipal context only.", i.Name))
			continue
		}
		out = append(out, practicalInstrumentMeaning(i))
	}
	return out
}

func practicalInstrumentMeaning(i model.Instrument) string {
	name := i.Name
	status := strings.TrimSpace(i.Status)
	base := fmt.Sprintf("%s applies at this location according to the available layer.", name)
	switch i.Family {
	case "albufeira":
		base += " In practical terms, this is a reservoir/water-protection plan: check buffers, permitted uses, construction limits, wastewater and vegetation rules before relying on the PDM alone."
	case "costa":
		base += " In practical terms, this is a coastal protection program: shoreline risk and safeguard rules may override or narrow what the PDM would otherwise allow."
	case "area-protegida":
		base += " In practical terms, protected-area conservation rules may add ICNF/special-plan requirements to ordinary municipal licensing."
	case "estuario":
		base += " In practical terms, estuary protection rules may add water, habitat and flood-risk restrictions to ordinary municipal licensing."
	default:
		base += " In practical terms, it can add special restrictions beyond the PDM."
	}
	if status != "" {
		base += " Registry status: " + status + "."
	}
	return base
}

func applicableArticleExplanations(reg *model.Regulation, analysedArea float64, zoning []model.ZoningHit, constraints []model.ConstraintHit) []string {
	if reg == nil || len(reg.Articles) == 0 {
		return nil
	}
	var out []string
	for _, a := range reg.Articles {
		if !articleRelevantToZoning(a, zoning) {
			continue
		}
		if s := articlePlainMeaning(a, analysedArea, zoning, constraints); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func articleRelevantToZoning(a model.Article, zoning []model.ZoningHit) bool {
	section := normalizeText(a.Section)
	if section == "" {
		return true
	}
	for _, z := range zoning {
		if containsAny(normalizeText(z.Label+" "+z.Class+" "+z.Subclass), strings.Fields(section)) {
			return true
		}
	}
	text := normalizeText(a.Title + " " + a.Text)
	if strings.Contains(text, "cedencias") || strings.Contains(text, "compensacoes") {
		return zoningLooksUrban(zoning)
	}
	return true
}

func articlePlainMeaning(a model.Article, analysedArea float64, zoning []model.ZoningHit, constraints []model.ConstraintHit) string {
	text := normalizeText(a.Title + " " + a.Text)
	ref := fmt.Sprintf("Article %s (%s)", a.Number, a.Title)
	switch {
	case a.Number == "52.º" && strings.Contains(text, "regime geral de ocupacao dos espacos agricolas"):
		return agriculturalGeneralOccupationArticleMeaning(ref, analysedArea)
	case a.Number == "54.º" && strings.Contains(text, "uso turistico") && strings.Contains(text, "espacos agricolas complementares"):
		return agriculturalTourismArticleMeaning(ref, analysedArea, zoning)
	case a.Number == "55.º" && strings.Contains(text, "castelo de bode"):
		return casteloBodeAgriculturalArticleMeaning(ref, constraints)
	case strings.Contains(text, "cedencias") || strings.Contains(text, "compensacoes"):
		if !zoningLooksUrban(zoning) {
			return ""
		}
		return ref + " means that, for a subdivision or a building operation with urban impact similar to a subdivision, the owner may have to give land to the municipality for roads, pavements, parking, infrastructure, green areas or public facilities, or pay compensation when physical cedence is not required or not possible."
	case strings.Contains(text, "usos") || strings.Contains(text, "utilizacoes") || strings.Contains(text, "atividades") || strings.Contains(text, "actividades"):
		return ref + " is mainly about permitted or compatible uses. In practical terms, compare the intended use with this article before assuming the land can be used or built for that purpose."
	case strings.Contains(text, "edificabilidade") || strings.Contains(text, "construcao") || strings.Contains(text, "construcoes") || strings.Contains(text, "obras"):
		return ref + " is mainly about building rules. In practical terms, it may affect whether construction is possible and under what limits, even when the zoning category looks favourable."
	case strings.Contains(text, "condicionamentos") || strings.Contains(text, "interdit") || strings.Contains(text, "proibid"):
		return ref + " is mainly about restrictions or prohibited acts. In practical terms, check it before any works, land-use change, clearing, earth movement or licensing request."
	default:
		return ""
	}
}

func agriculturalGeneralOccupationArticleMeaning(ref string, analysedArea float64) string {
	base := ref + " is the general occupation rule for agricultural land. In practical terms, it keeps agriculture as the main use and only admits limited cases such as agricultural support, renewable energy, tourism, or the farmer's own permanent dwelling. The farmer dwelling route requires a minimum parcel area of 4 ha (40,000 m²), among other conditions."
	if analysedArea > 0 && analysedArea < 40000 {
		base += fmt.Sprintf(" For this parcel, the analysed area is about %s m², below 4 ha, so this dwelling route does not fit this parcel unless a larger legally usable parcel area is demonstrated.", area(analysedArea))
	}
	return base
}

func agriculturalTourismArticleMeaning(ref string, analysedArea float64, zoning []model.ZoningHit) string {
	category := "Espaços Agrícolas Complementares"
	if z := primaryZoning(zoning); z != nil && strings.TrimSpace(z.Label) != "" {
		category = z.Label
	}
	base := ref + " is a conditional tourism rule for " + category + ". It does not make the parcel generally buildable: it only matters if there is a concrete tourism project associated with agricultural activity, and it requires a minimum parcel area of 4 ha (40,000 m²), with very low building intensity."
	if analysedArea > 0 && analysedArea < 40000 {
		base += fmt.Sprintf(" For this parcel, the analysed area is about %s m², below 4 ha, so this tourism route is not relevant unless a larger legally usable parcel area is demonstrated.", area(analysedArea))
	}
	return base
}

func casteloBodeAgriculturalArticleMeaning(ref string, constraints []model.ConstraintHit) string {
	if c, ok := findConstraint(constraints, "castelo de bode", "poacb", "albufeira"); ok && !c.Unknown && !c.Present {
		return ref + " only applies to agricultural land inside the protection zone of the Albufeira de Castelo de Bode/POACB. No POACB overlap was detected for this parcel/location in the checked layer, so this article is not applicable here."
	}
	if c, ok := findConstraint(constraints, "castelo de bode", "poacb", "albufeira"); ok && c.Present {
		return ref + " applies because the checked layer detected the Albufeira de Castelo de Bode/POACB area. It adds a stricter reservoir-protection regime: new construction is generally not allowed, and existing buildings are limited to the works and small extensions allowed by the article."
	}
	return ref + " only applies where agricultural land is inside the protection zone of the Albufeira de Castelo de Bode/POACB. If the POACB layer does not overlap the parcel, treat this article as not applicable to the parcel."
}

func primaryZoning(zoning []model.ZoningHit) *model.ZoningHit {
	if len(zoning) == 0 {
		return nil
	}
	best := &zoning[0]
	for i := range zoning[1:] {
		z := &zoning[i+1]
		if z.AreaM2 > best.AreaM2 {
			best = z
		}
	}
	return best
}

func findConstraint(constraints []model.ConstraintHit, terms ...string) (model.ConstraintHit, bool) {
	for _, c := range constraints {
		t := normalizeText(c.Type + " " + c.Label + " " + c.Detail + " " + c.Layer)
		for _, term := range terms {
			if strings.Contains(t, normalizeText(term)) {
				return c, true
			}
		}
	}
	return model.ConstraintHit{}, false
}

func zoningLooksUrban(zoning []model.ZoningHit) bool {
	for _, z := range zoning {
		t := normalizeText(z.Label + " " + z.Class + " " + z.Subclass)
		if strings.Contains(t, "urbano") || strings.Contains(t, "urbaniz") || strings.Contains(t, "aglomerado") || strings.Contains(t, "central") || strings.Contains(t, "habitacional") || strings.Contains(t, "equipamento") || strings.Contains(t, "industrial") {
			return true
		}
	}
	return false
}

func normalizeText(s string) string {
	s = strings.ToLower(s)
	repl := strings.NewReplacer(
		"á", "a", "à", "a", "ã", "a", "â", "a",
		"é", "e", "ê", "e",
		"í", "i",
		"ó", "o", "õ", "o", "ô", "o",
		"ú", "u",
		"ç", "c",
	)
	return repl.Replace(s)
}

func containsAny(s string, terms []string) bool {
	for _, term := range terms {
		term = strings.TrimSpace(term)
		if len(term) >= 4 && strings.Contains(s, term) {
			return true
		}
	}
	return false
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func isEnvelopeApprox(c model.ConstraintHit) bool {
	return strings.Contains(strings.ToLower(c.Note), "envelope")
}

// stateLabel renders an instrument's registry state for humans.
func stateLabel(state string) string {
	switch state {
	case "vigor":
		return "em vigor"
	case "parcial":
		return "parcialmente em vigor"
	case "elaboracao":
		return "em elaboração"
	case "revogado":
		return "revogado"
	case "nunca-aprovado":
		return "nunca aprovado"
	default:
		return state
	}
}

func instrumentEvidence(i model.Instrument) string {
	if i.PointInside != nil && *i.PointInside {
		return "confirmed at this location by a matching live layer"
	}
	return "municipality-level context; parcel/location overlap not confirmed by this report"
}

func writeInstruments(b *strings.Builder, ins []model.Instrument) {
	if len(ins) == 0 {
		return
	}
	fmt.Fprintf(b, "\nSpecial plans & programs in this municipality (context, not automatic parcel overlap):\n")
	for _, i := range ins {
		fmt.Fprintf(b, "  - [%s] %s — %s\n", stateLabel(i.State), i.Name, instrumentEvidence(i))
		if i.Diploma != "" {
			fmt.Fprintf(b, "      %s\n", i.Diploma)
		}
	}
	fmt.Fprintf(b, "  (statuses from the bundled registry, compiled from APA/ICNF/DGT/DRE; details: --format json)\n")
}

// mdCell folds text for a markdown table cell: pipes escaped, newlines
// collapsed, so odd upstream attribute values never break the table.
func mdCell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	return strings.Join(strings.Fields(s), " ")
}

func mdInstruments(b *strings.Builder, ins []model.Instrument) {
	if len(ins) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## Special plans & programs in this municipality\n\n")
	fmt.Fprintf(b, "_Context only unless the last column says it was confirmed at this location._\n\n")
	fmt.Fprintf(b, "| Instrument | State | Diploma | Evidence |\n|---|---|---|---|\n")
	for _, i := range ins {
		fmt.Fprintf(b, "| %s | %s | %s | %s |\n", mdCell(i.Name), stateLabel(i.State), mdCell(i.Diploma), mdCell(instrumentEvidence(i)))
	}
}

func writeConstraintContext(b *strings.Builder, c model.ConstraintHit) {
	if ev := constraintEvidence(c); ev != "" {
		fmt.Fprintf(b, "    Evidence: %s\n", ev)
	}
	if m := constraintMeaning(c); m != "" && (c.Present || c.Unknown) {
		fmt.Fprintf(b, "    Meaning: %s\n", m)
	}
	if l := constraintLimitation(c); l != "" {
		fmt.Fprintf(b, "    Limitation: %s\n", l)
	}
	if n := constraintNextStep(c); n != "" {
		fmt.Fprintf(b, "    Next check: %s\n", n)
	}
}

func mdConstraintContext(b *strings.Builder, c model.ConstraintHit) {
	if ev := constraintEvidence(c); ev != "" {
		fmt.Fprintf(b, "  - **Evidence:** %s\n", ev)
	}
	if m := constraintMeaning(c); m != "" && (c.Present || c.Unknown) {
		fmt.Fprintf(b, "  - **Meaning:** %s\n", m)
	}
	if l := constraintLimitation(c); l != "" {
		fmt.Fprintf(b, "  - **Limitation:** %s\n", l)
	}
	if n := constraintNextStep(c); n != "" {
		fmt.Fprintf(b, "  - **Next check:** %s\n", n)
	}
}

func constraintMarkdownDetail(c model.ConstraintHit) string {
	parts := []string{}
	candidates := []string{c.Detail, constraintEvidence(c)}
	if c.Present || c.Unknown {
		candidates = append(candidates,
			constraintMeaning(c),
			constraintLimitation(c),
			constraintNextStep(c),
			c.Note,
		)
	}
	for _, s := range candidates {
		if strings.TrimSpace(s) != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " ")
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
	mdIntro(b, pointIntro(r))
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
		if c.Note != "" {
			fmt.Fprintf(b, "  - _%s_\n", c.Note)
		}
		mdConstraintContext(b, c)
	}
	mdInstruments(b, r.Instruments)
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
	writeIntro(b, polygonIntro(r))
	fmt.Fprintf(b, "\nZoning:\n")
	if len(r.Zoning) == 0 {
		fmt.Fprintf(b, "  - (none matched)\n")
	}
	for _, z := range r.Zoning {
		fmt.Fprintf(b, "  - %s m² / %s%% — %s\n", area(z.AreaM2), pct(z.Percent), z.Label)
	}
	fmt.Fprintf(b, "\nConstraints:\n")
	if len(r.Constraints) == 0 {
		fmt.Fprintf(b, "  - (none evaluated)\n")
	}
	for _, c := range r.Constraints {
		switch {
		case c.AreaM2 > 0:
			fmt.Fprintf(b, "  - %s m² / %s%% — %s\n", area(c.AreaM2), pct(c.Percent), c.Type)
		default:
			fmt.Fprintf(b, "  - %s\n", constraintPointLine(c))
		}
		if c.Note != "" {
			fmt.Fprintf(b, "    (%s)\n", c.Note)
		}
		writeConstraintContext(b, c)
	}
	writeInstruments(b, r.Instruments)
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
		mdIntro(b, polygonIntro(r))
		fmt.Fprintf(b, "\n## Zoning\n\n")
		if len(r.Zoning) == 0 {
			fmt.Fprintf(b, "- (none matched)\n")
		} else {
			fmt.Fprintf(b, "| Area (m²) | %% | Category |\n|---:|---:|---|\n")
			for _, z := range r.Zoning {
				fmt.Fprintf(b, "| %s | %s%% | %s |\n", area(z.AreaM2), pct(z.Percent), z.Label)
			}
		}
		fmt.Fprintf(b, "\n## Constraints\n\n")
		if len(r.Constraints) == 0 {
			fmt.Fprintf(b, "- (none evaluated)\n")
		} else {
			fmt.Fprintf(b, "| Constraint | Finding | Area (m²) | %% | What it means / next check |\n|---|---|---:|---:|---|\n")
			for _, c := range r.Constraints {
				presence := constraintPresence(c)
				areaCell, pctCell := "—", "—"
				if c.AreaM2 > 0 {
					areaCell, pctCell = area(c.AreaM2), pct(c.Percent)+"%"
				}
				detail := constraintMarkdownDetail(c)
				fmt.Fprintf(b, "| %s | %s | %s | %s | %s |\n", mdCell(c.Type), presence, areaCell, pctCell, mdCell(detail))
			}
		}
		mdInstruments(b, r.Instruments)
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

func writeIntro(b *strings.Builder, intro reportIntro) {
	if intro.Lead == "" && len(intro.Bullets) == 0 && len(intro.Articles) == 0 {
		return
	}
	fmt.Fprintf(b, "\nPlain-language summary:\n")
	if intro.Lead != "" {
		fmt.Fprintf(b, "  %s\n", intro.Lead)
	}
	for _, s := range intro.Bullets {
		fmt.Fprintf(b, "  - %s\n", s)
	}
	if len(intro.Articles) > 0 {
		fmt.Fprintf(b, "  Matched articles in practical terms:\n")
		for _, s := range intro.Articles {
			fmt.Fprintf(b, "  - %s\n", s)
		}
	}
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

func mdIntro(b *strings.Builder, intro reportIntro) {
	if intro.Lead == "" && len(intro.Bullets) == 0 && len(intro.Articles) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## Plain-language summary\n\n")
	if intro.Lead != "" {
		fmt.Fprintf(b, "%s\n\n", intro.Lead)
	}
	for _, s := range intro.Bullets {
		fmt.Fprintf(b, "- %s\n", s)
	}
	if len(intro.Articles) > 0 {
		fmt.Fprintf(b, "\n### Matched articles in practical terms\n\n")
		for _, s := range intro.Articles {
			fmt.Fprintf(b, "- %s\n", s)
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
