package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/bernardosimoes/pdm/internal/model"
)

// PointHTML writes a complete, self-contained HTML report for a point result,
// embedding the given map figure (inline SVG produced by the mapview package).
func PointHTML(w io.Writer, r *model.PointResult, mapFigure string) error {
	b := &strings.Builder{}
	title := fmt.Sprintf("PDM %s — %.5f, %.5f", h(r.Municipality), r.Input.Lat, r.Input.Lon)
	head(b, title)

	fmt.Fprintf(b, `<div class="eyebrow">%s%s</div>`, planEyebrow(r.Plan), confidenceChip(r.Confidence))
	fmt.Fprintf(b, `<h1>%.5f, %.5f</h1>`, r.Input.Lat, r.Input.Lon)
	fmt.Fprintf(b, `<p class="lede">Municipality of <strong>%s</strong>%s.</p>`, h(r.Municipality), planLede(r.Plan))

	if !r.Supported {
		notesHTML(b, r.Notes)
		disclaimerHTML(b, r.Disclaimer)
		return tail(w, b)
	}
	introHTML(b, pointIntro(r))
	if mapFigure != "" {
		b.WriteString(mapFigure)
	}

	// Zoning
	b.WriteString(`<section><h2>Zoning</h2>`)
	if len(r.Zoning) == 0 {
		b.WriteString(`<p class="muted">No zoning category matched.</p>`)
	}
	for _, z := range r.Zoning {
		fmt.Fprintf(b, `<div class="zrow"><span class="ztag">%s</span><span class="zsub">%s</span></div>`, h(zHead(z)), h(z.Subclass))
	}
	b.WriteString(`</section>`)

	// Constraints
	b.WriteString(`<section><h2>Constraints</h2>`)
	b.WriteString(`<p class="muted">Positive findings are regulatory risk flags for the queried location. Where a row says approximate, treat it as a warning to verify the official map extract before making a decision.</p><div class="chips">`)
	if len(r.Constraints) == 0 {
		b.WriteString(`<p class="muted">None evaluated for this municipality — see the notes below.</p>`)
	}
	for _, c := range r.Constraints {
		chipHit(b, c, "")
	}
	b.WriteString(`</div></section>`)

	instrumentsHTML(b, r.Instruments)
	regulationHTML(b, r.Regulation)
	sourcesHTML(b, r.Sources)
	notesHTML(b, r.Notes)
	disclaimerHTML(b, r.Disclaimer)
	return tail(w, b)
}

// PolygonHTML writes a complete, self-contained HTML report for a polygon result.
func PolygonHTML(w io.Writer, r *model.PolygonResult, mapFigure string) error {
	b := &strings.Builder{}
	head(b, fmt.Sprintf("PDM %s — parcel", h(r.Municipality)))

	fmt.Fprintf(b, `<div class="eyebrow">%s%s</div>`, planEyebrow(r.Plan), confidenceChip(r.Confidence))
	b.WriteString(`<h1>Parcel analysis</h1>`)
	fmt.Fprintf(b, `<p class="lede">Municipality of <strong>%s</strong>%s · analysed area <strong>%s m²</strong>.</p>`,
		h(r.Municipality), planLede(r.Plan), area(r.AnalysedAreaM2))

	if !r.Supported {
		notesHTML(b, r.Notes)
		disclaimerHTML(b, r.Disclaimer)
		return tail(w, b)
	}
	introHTML(b, polygonIntro(r))
	if mapFigure != "" {
		b.WriteString(mapFigure)
	}

	b.WriteString(`<section><h2>Zoning</h2>`)
	if len(r.Zoning) == 0 {
		b.WriteString(`<p class="muted">No zoning category matched.</p>`)
	} else {
		b.WriteString(`<table><thead><tr><th>Category</th><th class="num">Area</th><th class="num">%</th></tr></thead><tbody>`)
		for _, z := range r.Zoning {
			fmt.Fprintf(b, `<tr><td>%s</td><td class="num">%s m²</td><td class="num">%s%%</td></tr>`, h(z.Label), area(z.AreaM2), pct(z.Percent))
		}
		b.WriteString(`</tbody></table>`)
	}
	b.WriteString(`</section>`)

	b.WriteString(`<section><h2>Constraints</h2>`)
	b.WriteString(`<p class="muted">Positive findings are regulatory risk flags for the parcel. Approximate findings use the parcel envelope and do not measure the affected area.</p><div class="chips">`)
	if len(r.Constraints) == 0 {
		b.WriteString(`<p class="muted">None evaluated for this municipality — see the notes below.</p>`)
	}
	for _, c := range r.Constraints {
		extra := ""
		if c.Present && c.AreaM2 > 0 {
			extra = fmt.Sprintf("%s m² · %s%%", area(c.AreaM2), pct(c.Percent))
		}
		chipHit(b, c, extra)
	}
	b.WriteString(`</div></section>`)

	instrumentsHTML(b, r.Instruments)
	regulationHTML(b, r.Regulation)
	sourcesHTML(b, r.Sources)
	notesHTML(b, r.Notes)
	disclaimerHTML(b, r.Disclaimer)
	return tail(w, b)
}

// ---- shared building blocks ----

func zHead(z model.ZoningHit) string {
	if z.Class != "" {
		return z.Class
	}
	return z.Label
}

func planEyebrow(p *model.PlanInfo) string {
	if p == nil {
		return "Planning query"
	}
	return h(p.Name)
}

func planLede(p *model.PlanInfo) string {
	if p == nil || p.Name == "" {
		return ""
	}
	return ", under the " + h(p.Name)
}

func confidenceChip(c model.Confidence) string {
	return fmt.Sprintf(`<span class="conf conf-%s">confidence: %s</span>`, c, c)
}

func chipHit(b *strings.Builder, c model.ConstraintHit, extra string) {
	state, cls := "no", "chip-no"
	switch {
	case c.Unknown:
		state, cls = "unknown", "chip-unk"
	case c.Present:
		state, cls = constraintPresence(c), "chip-yes"
	}
	fmt.Fprintf(b, `<div class="chip %s"><span class="chip-k">%s</span><span class="chip-v">%s</span>`, cls, h(c.Type), state)
	if c.Detail != "" {
		fmt.Fprintf(b, `<span class="chip-d">%s</span>`, h(c.Detail))
	}
	if extra != "" {
		fmt.Fprintf(b, `<span class="chip-d">%s</span>`, h(extra))
	}
	if ev := constraintEvidence(c); ev != "" {
		fmt.Fprintf(b, `<span class="chip-note"><strong>Evidence:</strong> %s</span>`, h(ev))
	}
	if m := constraintMeaning(c); m != "" && (c.Present || c.Unknown) {
		fmt.Fprintf(b, `<span class="chip-note"><strong>Meaning:</strong> %s</span>`, h(m))
	}
	if l := constraintLimitation(c); l != "" {
		fmt.Fprintf(b, `<span class="chip-note"><strong>Limitation:</strong> %s</span>`, h(l))
	}
	if n := constraintNextStep(c); n != "" {
		fmt.Fprintf(b, `<span class="chip-note"><strong>Next check:</strong> %s</span>`, h(n))
	}
	if c.Note != "" && (c.Present || c.Unknown) {
		fmt.Fprintf(b, `<span class="chip-note"><strong>Source caveat:</strong> %s</span>`, h(c.Note))
	}
	b.WriteString(`</div>`)
}

func introHTML(b *strings.Builder, intro reportIntro) {
	if intro.Lead == "" && len(intro.Bullets) == 0 && len(intro.Articles) == 0 {
		return
	}
	b.WriteString(`<section class="intro"><h2>Plain-language summary</h2>`)
	if intro.Lead != "" {
		fmt.Fprintf(b, `<p>%s</p>`, h(intro.Lead))
	}
	if len(intro.Bullets) > 0 {
		b.WriteString(`<ul>`)
		for _, s := range intro.Bullets {
			fmt.Fprintf(b, `<li>%s</li>`, h(s))
		}
		b.WriteString(`</ul>`)
	}
	if len(intro.Articles) > 0 {
		b.WriteString(`<h3>Matched articles in practical terms</h3><ul>`)
		for _, s := range intro.Articles {
			fmt.Fprintf(b, `<li>%s</li>`, h(s))
		}
		b.WriteString(`</ul>`)
	}
	b.WriteString(`</section>`)
}

func instrumentsHTML(b *strings.Builder, ins []model.Instrument) {
	if len(ins) == 0 {
		return
	}
	b.WriteString(`<section><h2>Special plans &amp; programs in this municipality</h2>`)
	b.WriteString(`<p class="muted">These entries are municipal context from the bundled national registry (APA/ICNF/DGT/DRE). They do not mean the parcel is inside the instrument unless the evidence line says it was confirmed at this location.</p><ul class="instruments">`)
	for _, i := range ins {
		fmt.Fprintf(b, `<li class="ins ins-%s"><span class="ins-state">%s</span><span class="ins-name">%s</span>`,
			h(i.State), h(stateLabel(i.State)), h(i.Name))
		fmt.Fprintf(b, `<span class="ins-here">%s</span>`, h(instrumentEvidence(i)))
		if i.Diploma != "" {
			fmt.Fprintf(b, `<span class="ins-dip">%s</span>`, h(i.Diploma))
		}
		if i.Status != "" {
			fmt.Fprintf(b, `<span class="ins-status">%s</span>`, h(i.Status))
		}
		b.WriteString(`</li>`)
	}
	b.WriteString(`</ul></section>`)
}

func regulationHTML(b *strings.Builder, reg *model.Regulation) {
	if reg == nil {
		return
	}
	b.WriteString(`<section><h2>Applicable regulation</h2>`)
	b.WriteString(`<p class="muted">Articles matched to the zoning category — read them in full. Retrieval only, not an interpretation or legal advice.</p>`)
	if reg.Reference != "" {
		if reg.URL != "" {
			fmt.Fprintf(b, `<p class="src-line">Source: <a href="%s">%s</a></p>`, h(reg.URL), h(reg.Reference))
		} else {
			fmt.Fprintf(b, `<p class="src-line">Source: %s</p>`, h(reg.Reference))
		}
	}
	if len(reg.Articles) == 0 {
		b.WriteString(`<p class="muted">No section-specific article matched; consult the full regulation.</p>`)
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

func sourcesHTML(b *strings.Builder, sources []model.Source) {
	b.WriteString(`<section><h2>Sources</h2><ul class="srcs">`)
	for _, s := range sources {
		line := h(s.Name)
		if s.URL != "" {
			line = fmt.Sprintf(`<a href="%s">%s</a>`, h(s.URL), h(s.Name))
		}
		fmt.Fprintf(b, `<li>%s <span class="prov">%s</span></li>`, line, h(string(s.Provenance)))
	}
	b.WriteString(`</ul></section>`)
}

func notesHTML(b *strings.Builder, notes []string) {
	if len(notes) == 0 {
		return
	}
	b.WriteString(`<section class="notes"><h2>Notes</h2><ul>`)
	for _, n := range notes {
		fmt.Fprintf(b, `<li>%s</li>`, h(n))
	}
	b.WriteString(`</ul></section>`)
}

func disclaimerHTML(b *strings.Builder, d string) {
	if d == "" {
		return
	}
	fmt.Fprintf(b, `<footer>%s</footer>`, h(d))
}

// artID derives a stable fragment id for an article number, so analysis
// citations can deep-link to the verbatim article.
func artID(number string) string {
	return "art-" + strings.ReplaceAll(number, " ", "-")
}

func h(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

func head(b *strings.Builder, title string) {
	b.WriteString("<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\"><title>")
	b.WriteString(h(title))
	b.WriteString("</title><style>")
	b.WriteString(css)
	b.WriteString("</style></head><body><main class=\"wrap\">")
}

func tail(w io.Writer, b *strings.Builder) error {
	b.WriteString("</main></body></html>")
	_, err := io.WriteString(w, b.String())
	return err
}

const css = `
:root{
 --bg:#f2efe8;--panel:#faf8f3;--ink:#20303a;--muted:#6a7780;--hair:#ddd5c7;--accent:#2a7f9e;
 --good:#2f7d5b;--good-bg:#e4f1ea;--warn:#b4562a;--warn-bg:#f6e7dd;
 --c-boundary:#5a6a74;--c-zoning:#8a6fb0;--c-ran:#4e8a4a;--c-ren:#3f7fbf;--c-poacb:#2a8fa8;--c-subject:#d8442a;
 --c-natura:#6d8f2f;--c-incendio:#c2652a;
}
@media (prefers-color-scheme:dark){:root{
 --bg:#0d1418;--panel:#131e24;--ink:#e2e9ed;--muted:#93a4ac;--hair:#26343c;--accent:#3fa8c9;
 --good:#57c491;--good-bg:#123024;--warn:#e6905e;--warn-bg:#33201400;--warn-bg:#331f14;
 --c-boundary:#8ea0ab;--c-zoning:#b498dd;--c-ran:#6fc06a;--c-ren:#6aa8e6;--c-poacb:#45bcd6;--c-subject:#ff6a4d;
 --c-natura:#9dc356;--c-incendio:#e8915a;
}}
:root[data-theme="light"]{--bg:#f2efe8;--panel:#faf8f3;--ink:#20303a;--muted:#6a7780;--hair:#ddd5c7;--good-bg:#e4f1ea;--warn-bg:#f6e7dd;--c-subject:#d8442a}
:root[data-theme="dark"]{--bg:#0d1418;--panel:#131e24;--ink:#e2e9ed;--muted:#93a4ac;--hair:#26343c;--good-bg:#123024;--warn-bg:#331f14;--c-subject:#ff6a4d}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--ink);line-height:1.55;
 font-family:ui-sans-serif,-apple-system,"Segoe UI",Roboto,sans-serif;-webkit-font-smoothing:antialiased}
.wrap{max-width:760px;margin:0 auto;padding:clamp(20px,4vw,44px)}
.eyebrow{display:flex;flex-wrap:wrap;gap:10px;align-items:center;font:600 12px/1.4 ui-monospace,Menlo,monospace;
 letter-spacing:.1em;text-transform:uppercase;color:var(--accent)}
h1{font-size:clamp(26px,5vw,36px);line-height:1.1;margin:.35em 0 .12em;letter-spacing:-.02em;
 font-variant-numeric:tabular-nums;text-wrap:balance}
.lede{color:var(--muted);margin:.1em 0 1.2em;max-width:60ch}
h2{font-size:14px;text-transform:uppercase;letter-spacing:.08em;color:var(--muted);
 margin:0 0 12px;padding-bottom:8px;border-bottom:1px solid var(--hair)}
section{margin:26px 0}
.muted{color:var(--muted)}.num{text-align:right;font-variant-numeric:tabular-nums}
.conf{font:600 11px ui-monospace,Menlo,monospace;text-transform:none;letter-spacing:.02em;
 padding:.25em .6em;border-radius:999px;border:1px solid var(--hair);color:var(--muted)}
.conf-high{color:var(--good)}.conf-medium{color:var(--accent)}
/* map */
.pdm-map{position:relative;margin:0 0 8px;background:var(--panel);border:1px solid var(--hair);border-radius:14px;padding:12px;overflow:hidden}
.pdm-map svg{display:block;width:100%;height:auto}
/* satellite basemap + Map/Satellite toggle */
.sat-img{display:none}
.pdm-map:has(.sat-toggle:checked) .sat-img{display:block}
.pdm-map:has(.sat-toggle:checked) .ly{fill-opacity:.14}
.pdm-map:has(.sat-toggle:checked) .ly[data-role="present"]{fill-opacity:.24}
.pdm-map:has(.sat-toggle:checked) .ly{stroke-width:2}
.pdm-map:has(.sat-toggle:checked) .map-scale line{stroke:#fff}
.pdm-map:has(.sat-toggle:checked) .map-scale text,.pdm-map:has(.sat-toggle:checked) .map-n,.pdm-map:has(.sat-toggle:checked) .map-narrow{fill:#fff}
.map-toggle{position:absolute;top:18px;left:18px;z-index:2}
.map-toggle .sat-toggle{position:absolute;width:1px;height:1px;opacity:0;pointer-events:none}
.map-toggle label{display:inline-flex;cursor:pointer;border:1px solid var(--hair);border-radius:8px;overflow:hidden;
 background:var(--panel);box-shadow:0 1px 3px rgba(0,0,0,.14);font:600 11px ui-monospace,Menlo,monospace}
.map-toggle .seg{padding:.4em .7em;color:var(--muted);user-select:none}
.map-toggle .seg-sat{border-left:1px solid var(--hair)}
.pdm-map:not(:has(.sat-toggle:checked)) .seg-map{background:var(--accent);color:#fff}
.pdm-map:has(.sat-toggle:checked) .seg-sat{background:var(--accent);color:#fff}
.map-toggle .sat-toggle:focus-visible+label{outline:2px solid var(--accent);outline-offset:2px}
.ly{stroke-width:1.3;stroke-linejoin:round;vector-effect:non-scaling-stroke;fill-opacity:.32}
.ly-boundary{fill:none;stroke:var(--c-boundary);stroke-width:1.7}
.ly-zoning{fill:var(--c-zoning);stroke:var(--c-zoning);fill-opacity:.22}
.ly-ran{fill:var(--c-ran);stroke:var(--c-ran)}
.ly-ren{fill:var(--c-ren);stroke:var(--c-ren)}
.ly-poacb{fill:var(--c-poacb);stroke:var(--c-poacb)}
.ly-zpe,.ly-zec{fill:var(--c-natura);stroke:var(--c-natura)}
.ly-incendio{fill:var(--c-incendio);stroke:var(--c-incendio)}
.ly[data-role="absent"]{fill-opacity:.12;stroke-dasharray:4 3}
.ly[data-role="present"]{fill-opacity:.42}
.subject-poly{fill:none;stroke:var(--c-subject);stroke-width:2.4;stroke-dasharray:6 3}
.subject-ring{fill:var(--c-subject);opacity:.18}
.subject-dot{fill:var(--c-subject);stroke:var(--panel);stroke-width:2}
.map-scale line{stroke:var(--ink);stroke-width:2}
.map-scale text,.map-n{font:600 11px ui-monospace,Menlo,monospace;fill:var(--ink)}
.map-narrow{fill:var(--ink)}
.pdm-legend{display:flex;flex-wrap:wrap;gap:10px 18px;margin-top:12px;padding-top:11px;
 border-top:1px solid var(--hair);font-size:13px;color:var(--muted)}
.pdm-legend span{display:inline-flex;align-items:center;gap:.5em}
.key{width:16px;height:11px;border-radius:3px;display:inline-block;border:1px solid transparent}
.key-subject{background:var(--c-subject);border-radius:50%;width:12px;height:12px}
.key.ly-boundary{background:transparent;border-color:var(--c-boundary)}
.key.ly-zoning{background:var(--c-zoning);opacity:.5}
.key.ly-ran{background:var(--c-ran)}.key.ly-ren{background:var(--c-ren)}.key.ly-poacb{background:var(--c-poacb)}
.key.ly-zpe,.key.ly-zec{background:var(--c-natura)}.key.ly-incendio{background:var(--c-incendio)}
.key[data-role="absent"]{opacity:.45}
/* zoning */
.zrow{display:flex;flex-wrap:wrap;align-items:baseline;gap:8px 12px;padding:9px 0;border-bottom:1px solid var(--hair)}
.ztag{font-weight:600}.zsub{color:var(--muted)}
table{width:100%;border-collapse:collapse;font-size:14.5px}
th{font:600 11px ui-monospace,Menlo,monospace;text-transform:uppercase;letter-spacing:.06em;color:var(--muted);
 text-align:left;padding:6px 8px;border-bottom:1px solid var(--hair)}
td{padding:8px;border-bottom:1px solid var(--hair);font-variant-numeric:tabular-nums}
/* chips */
.chips{display:flex;flex-wrap:wrap;gap:10px}
.chip{display:inline-flex;align-items:flex-start;gap:8px 10px;padding:.65em .85em;border-radius:10px;border:1px solid var(--hair);
 background:var(--panel);font-size:14px;max-width:100%}
.chip-k{font-weight:600}
.chip-v{font:600 12px ui-monospace,Menlo,monospace;padding:.12em .5em;border-radius:6px}
.chip-yes .chip-v{background:var(--warn-bg);color:var(--warn)}
.chip-no .chip-v{background:var(--good-bg);color:var(--good)}
.chip-unk .chip-v{background:var(--bg);color:var(--muted);border:1px dashed var(--hair)}
.chip-d{color:var(--muted);font-size:12.5px}
.chip-note{flex-basis:100%;color:var(--muted);font-size:12px}
.chip-note strong{color:var(--ink);font-weight:600}
/* special instruments */
.instruments{list-style:none;padding:0;margin:0}
.ins{display:flex;flex-wrap:wrap;align-items:baseline;gap:6px 10px;padding:10px 0;border-bottom:1px solid var(--hair);font-size:14px}
.ins-state{font:600 10.5px ui-monospace,Menlo,monospace;text-transform:uppercase;letter-spacing:.05em;
 padding:.2em .55em;border-radius:6px;border:1px solid var(--hair);color:var(--muted);white-space:nowrap}
.ins-vigor .ins-state{color:var(--warn);background:var(--warn-bg);border-color:transparent}
.ins-parcial .ins-state{color:var(--warn)}
.ins-elaboracao .ins-state{color:var(--accent)}
.ins-name{font-weight:600}
.ins-here{font:600 11px ui-monospace,Menlo,monospace;color:var(--muted)}
.ins-dip{color:var(--muted);font-size:12.5px}
.ins-status{flex-basis:100%;color:var(--muted);font-size:12.5px}
/* regulation */
.art{border:1px solid var(--hair);border-radius:10px;margin:8px 0;background:var(--panel);overflow:hidden}
.art summary{cursor:pointer;padding:11px 14px;font-weight:600;list-style:none}
.art summary::-webkit-details-marker{display:none}
.art summary::before{content:"▸";color:var(--accent);margin-right:8px;font-size:12px}
.art[open] summary::before{content:"▾"}
.art-n{font:600 12px ui-monospace,Menlo,monospace;color:var(--accent);margin-right:6px}
.art-s{display:inline-block;font:500 11px ui-sans-serif,sans-serif;color:var(--muted);margin-left:6px}
.art-text{padding:0 14px 14px;white-space:pre-wrap;font-size:13.5px;color:var(--ink);border-top:1px solid var(--hair);padding-top:12px}
.src-line{font-size:13.5px;color:var(--muted)}
.srcs{list-style:none;padding:0;margin:0;font-size:14px}
.srcs li{padding:6px 0;border-bottom:1px solid var(--hair)}
.prov{font:600 11px ui-monospace,Menlo,monospace;color:var(--muted);margin-left:6px}
.notes ul{padding-left:18px;color:var(--muted);font-size:14px}
.intro{background:var(--panel);border:1px solid var(--hair);border-radius:12px;padding:16px 18px}
.intro p{margin:0 0 10px;color:var(--ink)}
.intro ul{margin:0;padding-left:18px;color:var(--ink);font-size:14px}
.intro li{margin:7px 0}
.intro h3{font-size:13px;margin:14px 0 8px;color:var(--muted);text-transform:uppercase;letter-spacing:.06em}
/* AI analysis */
.ai-badge{color:var(--accent)}
.viability{display:flex;gap:14px;align-items:flex-start;margin:18px 0;padding:14px 16px;border:1px solid var(--hair);
 border-radius:14px;background:var(--panel)}
.viability p{margin:0;font-size:15px}
.v-tag{flex:none;font:600 12px ui-monospace,Menlo,monospace;text-transform:uppercase;letter-spacing:.06em;
 padding:.3em .7em;border-radius:999px;border:1px solid var(--hair)}
.v-favoravel .v-tag{background:var(--good-bg);color:var(--good)}
.v-condicionado .v-tag,.v-desfavoravel .v-tag{background:var(--warn-bg);color:var(--warn)}
.v-indeterminado .v-tag{color:var(--muted)}
.ai-sec{margin:18px 0}
.ai-sec h3{font-size:15px;margin:0 0 6px}
.ai-sec p{margin:.4em 0;font-size:14.5px}
.ai-notes{color:var(--muted);font-size:13.5px;border-left:2px solid var(--hair);padding-left:10px}
.loc th{width:40%;font-weight:600;text-transform:none;letter-spacing:0;font-family:inherit;font-size:14px;color:var(--ink)}
.cites{list-style:none;padding:0;margin:0;font-size:14px}
.cites li{padding:6px 0;border-bottom:1px solid var(--hair)}
a{color:var(--accent)}
footer{margin-top:28px;padding-top:16px;border-top:1px solid var(--hair);color:var(--muted);font-size:12.5px;line-height:1.6}
@media (prefers-reduced-motion:reduce){*{transition:none!important}}
`
