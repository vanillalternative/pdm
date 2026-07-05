// Command pdmdata regenerates the bundled snapshot data under ./data from the
// official DGT/SNIT and municipal geoservices. It fetches vector layers, reduces
// their (absurd) coordinate precision, simplifies administrative boundaries, and
// filters attributes to the fields the tool uses — producing compact GeoJSON
// suitable for embedding. Every output carries a "_source" provenance block.
//
// Usage:
//
//	go run ./cmd/pdmdata            # regenerate everything into ./data
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
		{"zoning", buildZoning},
		{"ran", buildRAN},
		{"ren", buildREN},
		{"poacb", buildPOACB},
		{"regulamento", buildRegulamento},
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
	u := ogcBase + "/municipios/items?" + url.Values{
		"bbox":  {"-8.95,39.30,-8.00,39.95"},
		"limit": {"200"},
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
		RetrievedAt: stamp, Note: "Region subset; boundaries simplified (~30m) for municipality resolution.",
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

const regPDFURL = "https://files.dre.pt/2s/2022/01/016000000/0032700390.pdf"

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

func buildRegulamento(stamp string) error {
	pdf, err := get(regPDFURL)
	if err != nil {
		return err
	}
	txt, err := pdfToText(pdf)
	if err != nil {
		return fmt.Errorf("pdftotext (install poppler): %w", err)
	}
	articles := parseArticles(txt)
	if len(articles) < 50 {
		return fmt.Errorf("parsed only %d articles — regulamento format may have changed", len(articles))
	}
	doc := map[string]any{
		"_source": source{
			Name: "PDM de Tomar — Regulamento (Aviso n.º 1510/2022, DR 2.ª série n.º 16)",
			URL:  regPDFURL, Service: "Diário da República (PDF → text)",
			RetrievedAt: stamp, Note: "Articles parsed from the official regulamento; section context preserved.",
		},
		"reference": "Aviso n.º 1510/2022, DR 2.ª série, n.º 16, de 2022-01-24",
		"articles":  articles,
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	if err := os.WriteFile("data/tomar/regulamento.json", b, 0o644); err != nil {
		return err
	}
	info, _ := os.Stat("data/tomar/regulamento.json")
	fmt.Printf("    wrote data/tomar/regulamento.json (%d articles, %.2f MB)\n", len(articles), float64(info.Size())/(1<<20))
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

func get(u string) ([]byte, error) {
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
