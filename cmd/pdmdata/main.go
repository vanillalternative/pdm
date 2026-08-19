// Command pdmdata regenerates the bundled snapshot data under ./data from the
// official DGT/SNIT and municipal geoservices. It fetches vector layers, reduces
// their (absurd) coordinate precision, simplifies administrative boundaries, and
// filters attributes to the fields the tool uses — producing compact GeoJSON
// suitable for embedding. Every output carries a "_source" provenance block.
//
// Usage:
//
//	go run ./cmd/pdmdata                                   # regenerate everything into ./data
//	go run ./cmd/pdmdata <target>                          # regenerate a single Tomar-pilot layer
//	go run ./cmd/pdmdata regulamento <dtcc> <pdf-url> <ref>  # fetch+parse one municipality's regulamento
//
// The `regulamento <dtcc> <url> <ref>` form is the nationwide path: it parses a
// single municipality's PDM written regulation from its Diário da República PDF
// into data/regulamentos/<dtcc>.json and upserts the coverage manifest
// data/regulamentos/index.json. It is what the fetch-regulamentos skill drives,
// one batch of municipalities at a time.
//
// This is a maintainer tool; it is not part of the shipped `pdm` binary.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/peterstace/simplefeatures/geom"
)

const (
	ogcBase   = "https://ogcapi.dgterritorio.gov.pt/collections"
	arcBase   = "https://geoportal.mediotejo.pt/arcgis/rest/services/CMTOMAR"
	tomarDTCC = "1418"
)

var httpClient = &http.Client{Timeout: 180 * time.Second}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	stamp := time.Now().UTC().Format(time.RFC3339)

	// Nationwide path: `regulamento <dtcc> <pdf-url> <reference>` parses one
	// municipality's regulamento and upserts the coverage manifest.
	if len(os.Args) > 1 && os.Args[1] == "regulamento" {
		if len(os.Args) != 5 {
			return fmt.Errorf("usage: pdmdata regulamento <dtcc> <pdf-url> <reference>")
		}
		return buildRegulamentoFor(os.Args[2], os.Args[3], os.Args[4], stamp)
	}

	// Optional: `go run ./cmd/pdmdata <target>` regenerates a single layer.
	only := ""
	if len(os.Args) > 1 {
		only = os.Args[1]
	}
	targets := []struct {
		name string
		fn   func(string) error
	}{
		{"municipalities", buildMunicipalities},
		{"freguesias", buildFreguesias},
		{"zoning", buildZoning},
		{"ran", buildRAN},
		{"ren", buildREN},
		{"poacb", buildPOACB},
		{"regulamento", buildTomarRegulamento},
	}
	for _, t := range targets {
		if only != "" && only != t.name {
			continue
		}
		fmt.Printf("[%s]\n", t.name)
		if err := t.fn(stamp); err != nil {
			return fmt.Errorf("%s: %w", t.name, err)
		}
	}
	fmt.Println("done.")
	return nil
}

// ---- targets ----

func buildMunicipalities(stamp string) error {
	// All mainland municipalities. The DGT OGC API carries CAOP Continente only;
	// the autonomous regions publish through their own regional services. Small
	// pages: full-detail CAOP boundaries run ~0.5 MB per municipality.
	u := ogcBase + "/municipios/items?" + url.Values{
		"limit": {"20"},
		"f":     {"json"},
	}.Encode()
	feats, err := fetchOGC(u)
	if err != nil {
		return err
	}
	out := process(feats, opts{
		keep:      []string{"municipio", "dtmn", "distrito_ilha"},
		precision: 5,
		simplify:  0.0003, // ~30 m — resolution only needs coarse borders
	})
	return writeFC("data/municipalities.geojson", out, source{
		Name: "DGT — CAOP (Carta Administrativa Oficial de Portugal), municípios",
		URL:  ogcBase + "/municipios", Service: "OGC API Features",
		RetrievedAt: stamp, Note: "All mainland municipalities (CAOP Continente); boundaries simplified (~30m) for municipality resolution.",
	})
}

func buildFreguesias(stamp string) error {
	// All mainland freguesias (~3000 features). Simplified more aggressively
	// than municipalities: freguesia resolution only labels a report, so ~50 m
	// borders are plenty and keep the embedded file small.
	u := ogcBase + "/freguesias/items?" + url.Values{
		"limit": {"100"},
		"f":     {"json"},
	}.Encode()
	feats, err := fetchOGC(u)
	if err != nil {
		return err
	}
	out := process(feats, opts{
		keep:      []string{"freguesia", "dtmnfr"},
		precision: 5,
		simplify:  0.0005, // ~50 m
	})
	return writeFC("data/freguesias.geojson", out, source{
		Name: "DGT — CAOP (Carta Administrativa Oficial de Portugal), freguesias",
		URL:  ogcBase + "/freguesias", Service: "OGC API Features",
		RetrievedAt: stamp, Note: "All mainland freguesias (CAOP Continente); boundaries simplified (~50m) for freguesia labelling.",
	})
}

func buildZoning(stamp string) error {
	u := ogcBase + "/crus/items?" + url.Values{
		"dtcc":  {tomarDTCC},
		"limit": {"5000"},
		"f":     {"json"},
	}.Encode()
	feats, err := fetchOGC(u)
	if err != nil {
		return err
	}
	out := process(feats, opts{
		keep: []string{"classe_2021", "categoria_2021", "classificacao_e_qualificacao",
			"situacao_pdm", "codigo", "municipio"},
		precision: 6,
	})
	return writeFC("data/tomar/ordenamento.geojson", out, source{
		Name: "DGT — CRUS (Carta do Regime de Uso do Solo) — Tomar (PDM em vigor)",
		URL:  ogcBase + "/crus", Service: "OGC API Features",
		RetrievedAt: stamp, Note: "Classificação e qualificação do solo (equivalente à Planta de Ordenamento).",
	})
}

func buildRAN(stamp string) error {
	u := ogcBase + "/srup_ran/items?" + url.Values{
		"municipio": {"TOMAR"},
		"limit":     {"5000"},
		"f":         {"json"},
	}.Encode()
	feats, err := fetchOGC(u)
	if err != nil {
		return err
	}
	out := process(feats, opts{
		keep: []string{"designacao", "servidao", "tipologia", "serv_lei", "serv_dr",
			"serv_hiperligacao", "municipio"},
		precision: 6,
	})
	return writeFC("data/tomar/ran.geojson", out, source{
		Name: "DGT/SNIT — SRUP, Reserva Agrícola Nacional (RAN) — Tomar",
		URL:  ogcBase + "/srup_ran", Service: "OGC API Features",
		RetrievedAt: stamp,
	})
}

func buildREN(stamp string) error {
	// The national OGC REN collections ignore bbox/attribute filters and stream
	// the whole country, so we use the municipal ArcGIS service, combining the
	// substantive REN typology layers and tagging each with its typology.
	// Ordered so regenerated output is deterministic (stable git diffs).
	layers := []struct {
		id   int
		name string
	}{
		{4, "Áreas de Elevado Risco de Erosão Hídrica do Solo"},
		{5, "Zonas Ameaçadas pelas Cheias"},
		{6, "Áreas de Instabilidade de Vertentes"},
		{7, "Albufeiras (conetividade e coerência ecológica da REN)"},
		{8, "Cursos de Água e Respetivos Leitos e Margens"},
		{9, "Áreas Estratégicas de Proteção e Recarga de Aquíferos"},
	}
	var all []outFeature
	for _, l := range layers {
		id, name := l.id, l.name
		feats, err := fetchArcGIS(fmt.Sprintf("%s/CondicionantesREN2020/MapServer/%d", arcBase, id))
		if err != nil {
			return fmt.Errorf("REN layer %d: %w", id, err)
		}
		out := process(feats, opts{
			keep:      []string{"Tipologias", "Uso_Atual"},
			precision: 6,
			simplify:  0.0004, // ~40 m — REN margins are dense; keep the bundle small
			inject:    map[string]any{"tipologia": name, "servidao": "REN"},
		})
		fmt.Printf("    layer %d %q → %d features\n", id, name, len(out))
		all = append(all, out...)
	}
	return writeFC("data/tomar/ren.geojson", all, source{
		Name: "Município de Tomar / Médio Tejo (MuniSIG) — Planta de Condicionantes, REN",
		URL:  arcBase + "/CondicionantesREN2020/MapServer", Service: "ArcGIS REST (GeoJSON)",
		RetrievedAt: stamp, Note: "REN typology layers 4–9 combined; tagged by typology.",
	})
}

func buildPOACB(stamp string) error {
	// Área de Intervenção do Plano de Ordenamento da Albufeira de Castelo de Bode
	// (the "zona de proteção da Albufeira" the PDM Regulamento singles out).
	feats, err := fetchArcGIS(arcBase + "/OrdenamentoZonasProtecaoSalvaguarda2020/MapServer/4")
	if err != nil {
		return err
	}
	out := process(feats, opts{
		precision: 6,
		inject: map[string]any{
			"servidao":   "POACB",
			"designacao": "Área de Intervenção do POACB (Albufeira de Castelo de Bode)",
		},
	})
	return writeFC("data/tomar/poacb.geojson", out, source{
		Name:        "Município de Tomar / Médio Tejo (MuniSIG) — Área de Intervenção do POACB",
		URL:         arcBase + "/OrdenamentoZonasProtecaoSalvaguarda2020/MapServer/4",
		Service:     "ArcGIS REST (GeoJSON)",
		RetrievedAt: stamp,
		Note:        "Zona de proteção da Albufeira de Castelo de Bode (Plano de Ordenamento da Albufeira).",
	})
}

// ---- regulamento (PDM written regulation) ----

var (
	reArtigo = regexp.MustCompile(`^Artigo\s+(\d+)\.º`)
	reSeccao = regexp.MustCompile(`^(SUBSECÇÃO|SUB-SECÇÃO|SECÇÃO)\b`)
	reChap   = regexp.MustCompile(`^(TÍTULO|CAPÍTULO)\b`)
	reFooter = regexp.MustCompile(`^(Diário da República|PARTE|Pág\.|N\.º \d+|\d{1,4}|www\.dre\.pt)`)
)

type regArticle struct {
	Number  string `json:"number"`
	Title   string `json:"title"`
	Section string `json:"section,omitempty"`
	Text    string `json:"text"`
}

// buildTomarRegulamento regenerates Tomar's regulamento via the shared path
// (kept so the full `go run ./cmd/pdmdata` regen still covers dtcc 1418).
func buildTomarRegulamento(stamp string) error {
	return buildRegulamentoFor(
		"1418",
		"https://files.dre.pt/2s/2022/01/016000000/0032700390.pdf",
		"Aviso n.º 1510/2022, DR 2.ª série, n.º 16, de 2022-01-24",
		stamp,
	)
}

// buildRegulamentoFor fetches a municipality's regulamento PDF, parses it into
// articles, writes data/regulamentos/<dtcc>.json, and upserts the coverage
// manifest. A short parse (<50 articles) is a warning, not a hard failure: some
// small municipalities genuinely have brief regulamentos, and others need a
// parser tweak — either way the skill spot-checks the output before committing.
func buildRegulamentoFor(dtcc, pdfURL, reference, stamp string) error {
	if dtcc == "" || pdfURL == "" || reference == "" {
		return fmt.Errorf("dtcc, pdf-url and reference are all required")
	}
	pdf, err := get(pdfURL)
	if err != nil {
		return err
	}
	txt, err := pdfToText(pdf)
	if err != nil {
		return fmt.Errorf("pdftotext (install poppler): %w", err)
	}
	articles := parseArticles(txt)
	if len(articles) == 0 {
		return fmt.Errorf("parsed 0 articles from %s — wrong PDF or the parser needs a new heading pattern", pdfURL)
	}
	if len(articles) < 50 {
		fmt.Printf("    WARNING: parsed only %d articles — spot-check the output; the PDF layout may need a parser tweak\n", len(articles))
	}
	doc := map[string]any{
		"_source": source{
			Name: "PDM — Regulamento (" + reference + ")",
			URL:  pdfURL, Service: "Diário da República (PDF → text)",
			RetrievedAt: stamp, Note: "Articles parsed from the official regulamento; section context preserved.",
		},
		"reference": reference,
		"articles":  articles,
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	outPath := filepath.Join("data", "regulamentos", dtcc+".json")
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(outPath, b, 0o644); err != nil {
		return err
	}
	info, _ := os.Stat(outPath)
	fmt.Printf("    wrote %s (%d articles, %.2f MB)\n", outPath, len(articles), float64(info.Size())/(1<<20))
	return upsertRegIndex(dtcc, pdfURL, reference, stamp, len(articles))
}

// upsertRegIndex writes/refreshes the municipality's row in the coverage
// manifest data/regulamentos/index.json, preserving other rows and any manually
// curated fields (municipality name, status, alteracoes) on an existing row.
func upsertRegIndex(dtcc, pdfURL, reference, stamp string, articleCount int) error {
	const idxPath = "data/regulamentos/index.json"
	type row struct {
		DTCC         string   `json:"dtcc"`
		Municipality string   `json:"municipality"`
		Reference    string   `json:"reference"`
		URL          string   `json:"url"`
		RetrievedAt  string   `json:"retrieved_at"`
		ArticleCount int      `json:"article_count"`
		Status       string   `json:"status"`
		Alteracoes   []string `json:"alteracoes"`
	}
	type manifest struct {
		Note           string `json:"_note,omitempty"`
		Municipalities []row  `json:"municipalities"`
	}
	var m manifest
	if b, err := os.ReadFile(idxPath); err == nil {
		if err := json.Unmarshal(b, &m); err != nil {
			return fmt.Errorf("parse %s: %w", idxPath, err)
		}
	}
	if m.Note == "" {
		m.Note = "Coverage manifest for bundled PDM regulamentos. One row per municipality with a parsed regulamento in data/regulamentos/<dtcc>.json. Maintained by the fetch-regulamentos skill."
	}
	found := false
	for i := range m.Municipalities {
		if m.Municipalities[i].DTCC == dtcc {
			m.Municipalities[i].Reference = reference
			m.Municipalities[i].URL = pdfURL
			m.Municipalities[i].RetrievedAt = stamp
			m.Municipalities[i].ArticleCount = articleCount
			if m.Municipalities[i].Status == "" {
				m.Municipalities[i].Status = "in-force"
			}
			if m.Municipalities[i].Alteracoes == nil {
				m.Municipalities[i].Alteracoes = []string{}
			}
			found = true
			break
		}
	}
	if !found {
		m.Municipalities = append(m.Municipalities, row{
			DTCC: dtcc, Reference: reference, URL: pdfURL, RetrievedAt: stamp,
			ArticleCount: articleCount, Status: "in-force", Alteracoes: []string{},
		})
	}
	sort.Slice(m.Municipalities, func(i, j int) bool { return m.Municipalities[i].DTCC < m.Municipalities[j].DTCC })
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // keep "<dtcc>" and "&" readable in the manifest
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return err
	}
	if err := os.WriteFile(idxPath, []byte(buf.String()), 0o644); err != nil {
		return err
	}
	fmt.Printf("    upserted %s (dtcc %s)\n", idxPath, dtcc)
	return nil
}

func pdfToText(pdf []byte) (string, error) {
	tmp, err := os.CreateTemp("", "pdm-*.pdf")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(pdf); err != nil {
		tmp.Close()
		return "", err
	}
	tmp.Close()
	out, err := exec.Command("pdftotext", "-enc", "UTF-8", tmp.Name(), "-").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func parseArticles(txt string) []regArticle {
	var lines []string
	for _, l := range strings.Split(txt, "\n") {
		lines = append(lines, strings.TrimSpace(l))
	}
	nextNonEmpty := func(i int) (string, int) {
		for j := i + 1; j < len(lines); j++ {
			if lines[j] != "" {
				return lines[j], j
			}
		}
		return "", len(lines)
	}
	var arts []regArticle
	var section, chapter string
	for i := 0; i < len(lines); {
		l := lines[i]
		switch {
		case reChap.MatchString(l):
			t, j := nextNonEmpty(i)
			chapter, section = t, ""
			i = j + 1
		case reSeccao.MatchString(l):
			t, j := nextNonEmpty(i)
			section = t
			i = j + 1
		case reArtigo.MatchString(l):
			num := reArtigo.FindStringSubmatch(l)[1]
			title, j := nextNonEmpty(i)
			var body []string
			k := j + 1
			for k < len(lines) {
				if reArtigo.MatchString(lines[k]) || reSeccao.MatchString(lines[k]) || reChap.MatchString(lines[k]) {
					break
				}
				if lines[k] != "" && !reFooter.MatchString(lines[k]) {
					body = append(body, lines[k])
				}
				k++
			}
			sec := section
			if sec == "" {
				sec = chapter
			}
			arts = append(arts, regArticle{Number: num + ".º", Title: title, Section: sec, Text: strings.Join(body, "\n")})
			i = k
		default:
			i++
		}
	}
	return arts
}

// ---- processing ----

type opts struct {
	keep      []string
	precision int
	simplify  float64        // 0 = none
	inject    map[string]any // constant props added to every feature
}

type outFeature struct {
	Type       string          `json:"type"`
	Properties map[string]any  `json:"properties"`
	Geometry   json.RawMessage `json:"geometry"`
}

type inFeature struct {
	Properties map[string]any  `json:"properties"`
	Geometry   json.RawMessage `json:"geometry"`
}

func process(raw []json.RawMessage, o opts) []outFeature {
	var out []outFeature
	var skipped int
	for _, rf := range raw {
		var f inFeature
		if json.Unmarshal(rf, &f) != nil || len(f.Geometry) == 0 || string(f.Geometry) == "null" {
			skipped++
			continue
		}
		gjson, ok := reduceGeometry(f.Geometry, o.precision, o.simplify)
		if !ok {
			skipped++
			continue
		}
		props := map[string]any{}
		for _, k := range o.keep {
			if v, ok := f.Properties[k]; ok && v != nil {
				props[k] = v
			}
		}
		for k, v := range o.inject {
			props[k] = v
		}
		out = append(out, outFeature{Type: "Feature", Properties: props, Geometry: gjson})
	}
	if skipped > 0 {
		fmt.Printf("    (skipped %d features)\n", skipped)
	}
	return out
}

func reduceGeometry(gjson json.RawMessage, precision int, simplify float64) (json.RawMessage, bool) {
	g, err := geom.UnmarshalGeoJSON(gjson, geom.NoValidate{})
	if err != nil || g.IsEmpty() {
		return nil, false
	}
	if simplify > 0 {
		if s, err := g.Simplify(simplify, geom.NoValidate{}); err == nil && !s.IsEmpty() {
			g = s
		}
	}
	if precision > 0 {
		p := math.Pow10(precision)
		g = g.TransformXY(func(xy geom.XY) geom.XY {
			return geom.XY{X: math.Round(xy.X*p) / p, Y: math.Round(xy.Y*p) / p}
		})
	}
	b, err := g.MarshalJSON()
	if err != nil || g.IsEmpty() {
		return nil, false
	}
	return b, true
}

// ---- fetchers ----

// fetchOGC pulls all features from an OGC API Features items URL, following
// rel="next" links.
func fetchOGC(itemsURL string) ([]json.RawMessage, error) {
	var all []json.RawMessage
	next := itemsURL
	for page := 0; page < 50 && next != ""; page++ {
		body, err := get(next)
		if err != nil {
			return nil, err
		}
		var doc struct {
			Features []json.RawMessage `json:"features"`
			Links    []struct {
				Rel, Href, Type string
			} `json:"links"`
			NumberReturned int `json:"numberReturned"`
		}
		if err := json.Unmarshal(body, &doc); err != nil {
			return nil, err
		}
		all = append(all, doc.Features...)
		next = ""
		for _, l := range doc.Links {
			if l.Rel == "next" && (l.Type == "" || strings.Contains(l.Type, "json")) {
				next = l.Href
			}
		}
		// Stop only when the page is empty; rely on the next link for pagination
		// (numberReturned may be omitted by the server).
		if len(doc.Features) == 0 {
			break
		}
	}
	return all, nil
}

// fetchArcGIS pulls all features from an ArcGIS MapServer/FeatureServer layer as
// GeoJSON (reprojected to WGS84), paginating via resultOffset.
func fetchArcGIS(layerURL string) ([]json.RawMessage, error) {
	var all []json.RawMessage
	offset := 0
	const page = 1000
	for i := 0; i < 200; i++ {
		q := url.Values{
			"where":             {"1=1"},
			"outFields":         {"*"},
			"outSR":             {"4326"},
			"f":                 {"geojson"},
			"resultOffset":      {fmt.Sprint(offset)},
			"resultRecordCount": {fmt.Sprint(page)},
		}
		body, err := get(layerURL + "/query?" + q.Encode())
		if err != nil {
			return nil, err
		}
		var doc struct {
			Features              []json.RawMessage `json:"features"`
			ExceededTransferLimit bool              `json:"exceededTransferLimit"`
		}
		if err := json.Unmarshal(body, &doc); err != nil {
			return nil, err
		}
		all = append(all, doc.Features...)
		if !doc.ExceededTransferLimit || len(doc.Features) == 0 {
			break
		}
		offset += len(doc.Features)
	}
	return all, nil
}

// get fetches a URL, retrying with backoff: the public geoservices are slow to
// first byte when cold and intermittently answer 502 mid-pagination.
func get(u string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(5*attempt*attempt) * time.Second) // 5s,20s,45s,80s
			fmt.Printf("    (retrying %d/4)\n", attempt)
		}
		body, err := getOnce(u)
		if err == nil {
			return body, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func getOnce(u string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("bad URL %q: %w", u, err)
	}
	req.Header.Set("User-Agent", "pdmdata/0.1 (planning-data ingest)")
	req.Header.Set("Accept", "application/json, application/geo+json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d", u, resp.StatusCode)
	}
	return body, nil
}

// ---- output ----

type source struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	Service     string `json:"service"`
	RetrievedAt string `json:"retrieved_at"`
	Note        string `json:"note,omitempty"`
}

func writeFC(path string, feats []outFeature, src source) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	doc := map[string]any{
		"type":     "FeatureCollection",
		"_source":  src,
		"features": feats,
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return err
	}
	info, _ := os.Stat(path)
	fmt.Printf("    wrote %s (%d features, %.1f MB)\n", path, len(feats), float64(info.Size())/(1<<20))
	return nil
}
