package query_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bernardosimoes/pdm/data"
	"github.com/bernardosimoes/pdm/internal/admin"
	"github.com/bernardosimoes/pdm/internal/mapview"
	"github.com/bernardosimoes/pdm/internal/model"
	"github.com/bernardosimoes/pdm/internal/query"
	"github.com/bernardosimoes/pdm/internal/source"
	"github.com/bernardosimoes/pdm/internal/spatial"
	"github.com/peterstace/simplefeatures/geom"
)

func newEngine(t *testing.T) *query.Engine {
	t.Helper()
	// Tests must stay offline: any unexpected live fetch fails loudly.
	return newEngineOpts(t, source.Options{Live: false, HTTP: errClient(fmt.Errorf("network use in offline test"))})
}

// shortCtx bounds a test query so the fetch retry backoff (2s+4s per probe)
// does not stall offline tests: the first attempt of every fetch still runs
// (and fails loudly through the stub), only the retry sleeps are cut short.
// Bundled layers never touch the network, so they are unaffected.
func shortCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	t.Cleanup(cancel)
	return ctx
}

func newEngineOpts(t *testing.T, opts source.Options) *query.Engine {
	t.Helper()
	r, err := admin.NewResolver(data.Municipalities)
	if err != nil {
		t.Fatal(err)
	}
	return query.New(r, opts)
}

// rtFunc adapts a function to http.RoundTripper for stubbing live fetches.
type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func errClient(err error) *http.Client {
	return &http.Client{Transport: rtFunc(func(*http.Request) (*http.Response, error) {
		return nil, err
	})}
}

func jsonClient(t *testing.T, body string, sawURL *string) *http.Client {
	t.Helper()
	return &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		if sawURL != nil {
			*sawURL = r.URL.String()
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{"Content-Type": []string{"application/geo+json"}},
		}, nil
	})}
}

// emptyFC is what national services answer when nothing intersects the probe.
const emptyFC = `{"type":"FeatureCollection","numberMatched":0,"features":[]}`

// stubRoute maps a URL substring to a canned body; first match wins.
type stubRoute struct{ sub, body string }

// stubClient answers each request with the first route whose substring matches
// the URL (empty FeatureCollection otherwise) and records all URLs seen.
// Layers are evaluated concurrently, so recording is mutex-guarded.
func stubClient(t *testing.T, seen *[]string, routes []stubRoute) *http.Client {
	t.Helper()
	var mu sync.Mutex
	return &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		u := r.URL.String()
		if seen != nil {
			mu.Lock()
			*seen = append(*seen, u)
			mu.Unlock()
		}
		body := emptyFC
		for _, rt := range routes {
			if strings.Contains(u, rt.sub) {
				body = rt.body
				break
			}
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{"Content-Type": []string{"application/geo+json"}},
		}, nil
	})}
}

// genericRoutes are the stubbed national services for the Ourém tests: CRUS
// zoning plus RAN/REN existence checks that confirm the municipality has data,
// with every probe answering "nothing here".
func genericRoutes() []stubRoute {
	return []stubRoute{
		{"collections/crus/items", ouremCRUS},
		// Existence checks (attribute-only, no bbox) confirm data exists…
		{"srup_ran/items?f=json&limit=1&municipio=OUR", `{"numberMatched":1,"features":[{"type":"Feature","properties":{"municipio":"OURÉM"}}]}`},
		{"srup_ren_areal/items?dtcc=1421&f=json&limit=1", `{"numberMatched":1,"features":[{"type":"Feature","properties":{"dtcc":"1421"}}]}`},
		// …and the bbox probes find nothing at the point (default emptyFC).
	}
}

// Ferreira do Zêzere (dtcc 1411) has no dedicated adapter, so it exercises the
// generic CRUS path and the truth-mirror decoration. The test point is its
// centroid; every canned polygon below covers it.
const (
	fzLon, fzLat = -8.31687, 39.72135
	fzSquare     = `"geometry":{"type":"Polygon","coordinates":[[[-8.45,39.6],[-8.2,39.6],[-8.2,39.8],[-8.45,39.8],[-8.45,39.6]]]}`
)

// fzCRUS is a canned CRUS response covering the Ferreira do Zêzere point.
const fzCRUS = `{"type":"FeatureCollection","features":[{"type":"Feature","properties":{
"classe_2021":"Solo Rústico","categoria_2021":"Espaços Florestais",
"classificacao_e_qualificacao":"Solo Rústico - Espaços Florestais",
"dtcc":"1411","municipio":"FERREIRA DO ZÊZERE","codigo":31,"situacao_pdm":"Vigente"},` + fzSquare + `}]}`

// fzTruthHit is a truth-mirror response whose recorded polygon covers the
// point; fzTruthGap records only a polygon elsewhere (a coverage gap).
const fzTruthHit = `{"type":"FeatureCollection","features":[{"type":"Feature","properties":{
"class":"Solo rústico","subclass":"Espaços florestais","label":"Solo rústico — Espaços florestais (registado)",
"raw_code":"F1","color":"#4f8a3d","layer_id":"ordenamento","muni_code":"1411","recorded_at":"2026-08-01T10:00:00Z"},` + fzSquare + `}],
"pdms":{"count":1,"next_after":null,"recorded_from":["DGT/SNIT — CRUS (ordenamento do solo)"],"updated_at":"2026-08-02T12:30:00Z"}}`

const fzTruthGap = `{"type":"FeatureCollection","features":[{"type":"Feature","properties":{
"class":"Solo urbano","label":"Solo urbano"},
"geometry":{"type":"Polygon","coordinates":[[[-8.1,39.6],[-8.0,39.6],[-8.0,39.7],[-8.1,39.7],[-8.1,39.6]]]}}],
"pdms":{"count":1,"next_after":null,"recorded_from":[],"updated_at":"2026-08-02T12:30:00Z"}}`

// fzRoutes stubs the national services for Ferreira do Zêzere: CRUS zoning
// plus RAN/REN existence checks confirming data exists (probes answer empty).
func fzRoutes(truthBody string) []stubRoute {
	routes := []stubRoute{
		{"collections/crus/items", fzCRUS},
		{"srup_ran/items?f=json&limit=1&municipio=FERREIRA", `{"numberMatched":1,"features":[{"type":"Feature","properties":{"municipio":"FERREIRA DO ZÊZERE"}}]}`},
		{"srup_ren_areal/items?dtcc=1411&f=json&limit=1", `{"numberMatched":1,"features":[{"type":"Feature","properties":{"dtcc":"1411"}}]}`},
	}
	if truthBody != "" {
		routes = append([]stubRoute{{"/api/truth/zoning", truthBody}}, routes...)
	}
	return routes
}

// TestPointTruthMirrorHit: for a generic municipality with the mirror
// configured, the recorded answer is served, the official CRUS source is never
// contacted, and the result is honestly capped and annotated.
func TestPointTruthMirrorHit(t *testing.T) {
	var seen []string
	eng := newEngineOpts(t, source.Options{
		Live: false, HTTP: stubClient(t, &seen, fzRoutes(fzTruthHit)),
		TruthAPI: "http://mirror.local",
	})
	res, err := eng.Point(context.Background(), fzLon, fzLat)
	if err != nil {
		t.Fatal(err)
	}
	if res.Municipality != "Ferreira do Zêzere" || !res.Supported {
		t.Fatalf("expected supported Ferreira do Zêzere, got %q supported=%v", res.Municipality, res.Supported)
	}
	if len(res.Zoning) != 1 || res.Zoning[0].Label != "Solo rústico — Espaços florestais (registado)" {
		t.Fatalf("expected the recorded zoning label, got %+v", res.Zoning)
	}
	if res.Zoning[0].RawCode != "F1" {
		t.Errorf("expected the recorded raw_code, got %q", res.Zoning[0].RawCode)
	}
	var mirror *model.Source
	for i := range res.Sources {
		if res.Sources[i].Provenance == model.ProvenanceRecordedMirror {
			mirror = &res.Sources[i]
		}
	}
	if mirror == nil {
		t.Fatalf("expected a recorded-mirror source, got %+v", res.Sources)
	}
	if !strings.Contains(mirror.Name, "orig.:") {
		t.Errorf("mirror source should credit the original source, got %q", mirror.Name)
	}
	if !sawURLContaining(seen, "/api/truth/zoning", "code=1411") {
		t.Errorf("expected a truth-mirror request, got %v", seen)
	}
	if sawURLContaining(seen, "collections/crus/items") {
		t.Errorf("a mirror hit must not contact CRUS, got %v", seen)
	}
	if res.Confidence != model.ConfidenceMedium {
		t.Errorf("mirror-served results are capped at medium, got %s", res.Confidence)
	}
	if !hasNoteContaining(res.Notes, "espelho local pdms") {
		t.Errorf("expected the mirror note, got %v", res.Notes)
	}
}

// TestPointTruthMirrorGapFallsBack: a recorded polygon that does not cover the
// point is a miss — the official CRUS source answers, with no mirror trace.
func TestPointTruthMirrorGapFallsBack(t *testing.T) {
	var seen []string
	eng := newEngineOpts(t, source.Options{
		Live: false, HTTP: stubClient(t, &seen, fzRoutes(fzTruthGap)),
		TruthAPI: "http://mirror.local",
	})
	res, err := eng.Point(context.Background(), fzLon, fzLat)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Zoning) != 1 || res.Zoning[0].Label != "Solo Rústico - Espaços Florestais" {
		t.Fatalf("expected the CRUS zoning after a mirror gap, got %+v", res.Zoning)
	}
	if !sawURLContaining(seen, "/api/truth/zoning") || !sawURLContaining(seen, "collections/crus/items") {
		t.Fatalf("expected mirror then CRUS requests, got %v", seen)
	}
	if len(res.Sources) == 0 || res.Sources[0].Provenance != model.ProvenanceOfficialLive {
		t.Errorf("expected official-live zoning source, got %+v", res.Sources)
	}
	if hasNoteContaining(res.Notes, "unavailable") {
		t.Errorf("a clean fallback must not report the layer unavailable, got %v", res.Notes)
	}
	if hasNoteContaining(res.Notes, "espelho local pdms") {
		t.Errorf("a mirror miss must not carry the mirror note, got %v", res.Notes)
	}
}

// TestPointTruthMirror500FallsBack: a broken mirror is invisible — the
// official source answers as if the mirror did not exist.
func TestPointTruthMirror500FallsBack(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	inner := stubClient(t, nil, fzRoutes(""))
	client := &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		u := r.URL.String()
		mu.Lock()
		seen = append(seen, u)
		mu.Unlock()
		if strings.Contains(u, "/api/truth/") {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       io.NopCloser(strings.NewReader("boom")),
				Header:     http.Header{},
			}, nil
		}
		return inner.Transport.RoundTrip(r)
	})}
	eng := newEngineOpts(t, source.Options{Live: false, HTTP: client, TruthAPI: "http://mirror.local"})
	res, err := eng.Point(context.Background(), fzLon, fzLat)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Zoning) != 1 || res.Zoning[0].Label != "Solo Rústico - Espaços Florestais" {
		t.Fatalf("expected the CRUS zoning after a mirror error, got %+v", res.Zoning)
	}
	if !sawURLContaining(seen, "/api/truth/zoning") || !sawURLContaining(seen, "collections/crus/items") {
		t.Fatalf("expected mirror then CRUS requests, got %v", seen)
	}
}

// TestPointTruthMirrorLiveBypasses: --live means fresh official data — the
// mirror must not be consulted at all.
func TestPointTruthMirrorLiveBypasses(t *testing.T) {
	var seen []string
	eng := newEngineOpts(t, source.Options{
		Live: true, HTTP: stubClient(t, &seen, fzRoutes(fzTruthHit)),
		TruthAPI: "http://mirror.local",
	})
	res, err := eng.Point(context.Background(), fzLon, fzLat)
	if err != nil {
		t.Fatal(err)
	}
	if sawURLContaining(seen, "/api/truth/") {
		t.Errorf("--live must bypass the mirror, got %v", seen)
	}
	if len(res.Zoning) != 1 || res.Zoning[0].Label != "Solo Rústico - Espaços Florestais" {
		t.Fatalf("expected the CRUS zoning under --live, got %+v", res.Zoning)
	}
}

// TestPolygonTruthMirrorBypassed: the mirror never serves parcel queries (the
// store cannot prove full parcel coverage).
func TestPolygonTruthMirrorBypassed(t *testing.T) {
	var seen []string
	eng := newEngineOpts(t, source.Options{
		Live: false, HTTP: stubClient(t, &seen, fzRoutes(fzTruthHit)),
		TruthAPI: "http://mirror.local",
	})
	res, err := eng.Polygon(context.Background(), squareAround(t, fzLon, fzLat, 0.001))
	if err != nil {
		t.Fatal(err)
	}
	if sawURLContaining(seen, "/api/truth/") {
		t.Errorf("polygon queries must never consult the mirror, got %v", seen)
	}
	if !sawURLContaining(seen, "collections/crus/items") {
		t.Errorf("expected the CRUS request, got %v", seen)
	}
	if len(res.Zoning) != 1 || res.Zoning[0].Label != "Solo Rústico - Espaços Florestais" {
		t.Fatalf("unexpected zoning: %+v", res.Zoning)
	}
}

// TestPointStreamLayerEventSourceAndRawCode: streamed layer events carry the
// attribution of the data that answered them, and the zoning GeoJSON
// properties include the raw source code.
func TestPointStreamLayerEventSourceAndRawCode(t *testing.T) {
	eng := newEngineOpts(t, source.Options{
		Live: false, HTTP: stubClient(t, nil, fzRoutes(fzTruthHit)),
		TruthAPI: "http://mirror.local",
	})
	var mu sync.Mutex
	var zoningEvents []query.LayerEvent
	_, err := eng.PointStream(context.Background(), fzLon, fzLat, func(v any) {
		if ev, ok := v.(query.LayerEvent); ok && ev.ID == "ordenamento" {
			mu.Lock()
			zoningEvents = append(zoningEvents, ev)
			mu.Unlock()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(zoningEvents) != 1 {
		t.Fatalf("expected one zoning layer event, got %d", len(zoningEvents))
	}
	ev := zoningEvents[0]
	if ev.Source == nil || ev.Source.Provenance != model.ProvenanceRecordedMirror {
		t.Fatalf("expected a recorded-mirror source on the layer event, got %+v", ev.Source)
	}
	gj, err := json.Marshal(ev.ZoningGeoJSON)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gj), `"raw_code":"F1"`) {
		t.Errorf("zoning_geojson properties must include raw_code, got %s", gj)
	}
}

// TestPointDedicatedAdapterSkipsMirror: municipalities with a dedicated
// adapter (richer local sources) never consult the mirror.
func TestPointDedicatedAdapterSkipsMirror(t *testing.T) {
	var seen []string
	eng := newEngineOpts(t, source.Options{
		Live: false, HTTP: stubClient(t, &seen, genericRoutes()),
		TruthAPI: "http://mirror.local",
	})
	res, err := eng.Point(context.Background(), -8.60, 39.68) // Ourém
	if err != nil {
		t.Fatal(err)
	}
	if res.Municipality != "Ourém" {
		t.Fatalf("expected Ourém, got %q", res.Municipality)
	}
	if sawURLContaining(seen, "/api/truth/") {
		t.Errorf("a dedicated adapter must never consult the mirror, got %v", seen)
	}
}

func TestPointTomarCentre(t *testing.T) {
	res, err := newEngine(t).Point(shortCtx(t), -8.41, 39.60)
	if err != nil {
		t.Fatal(err)
	}
	if res.Municipality != "Tomar" || !res.Supported {
		t.Fatalf("expected supported Tomar, got %q supported=%v", res.Municipality, res.Supported)
	}
	if len(res.Zoning) == 0 {
		t.Error("expected at least one zoning hit at Tomar centre")
	}
	// The centre falls in an aquifer-recharge REN area and outside RAN.
	ren := findConstraint(res.Constraints, "REN")
	ran := findConstraint(res.Constraints, "RAN")
	if ren == nil || !ren.Present {
		t.Error("expected REN present at Tomar centre")
	}
	if ran == nil || ran.Present {
		t.Error("expected RAN absent at Tomar centre")
	}
	if res.Plan == nil || res.Plan.Name != "PDM de Tomar" {
		t.Error("expected PDM de Tomar plan")
	}
	// Regulation retrieval: the centre's zoning category should surface its
	// regulation articles with full text, and the tool must not interpret them.
	if res.Regulation == nil || len(res.Regulation.Articles) == 0 {
		t.Fatal("expected applicable regulation articles at Tomar centre")
	}
	if res.Regulation.Articles[0].Text == "" {
		t.Error("regulation articles should carry verbatim text (the AI payload)")
	}
}

func TestPOACBConstraint(t *testing.T) {
	eng := newEngine(t)
	// Inside the Área de Intervenção do POACB (Albufeira de Castelo de Bode).
	in, err := eng.Point(shortCtx(t), -8.32522, 39.54334)
	if err != nil {
		t.Fatal(err)
	}
	if c := findConstraint(in.Constraints, "Albufeira Castelo de Bode"); c == nil || !c.Present {
		t.Errorf("expected POACB present at interior point, got %+v", c)
	}
	// Just outside it (~500 m).
	out, err := eng.Point(shortCtx(t), -8.27746, 39.58687)
	if err != nil {
		t.Fatal(err)
	}
	if c := findConstraint(out.Constraints, "Albufeira Castelo de Bode"); c == nil || c.Present {
		t.Errorf("expected POACB absent just outside, got %+v", c)
	}
}

// ouremCRUS is a canned CRUS response covering the Ourém test point.
const ouremCRUS = `{"type":"FeatureCollection","features":[{"type":"Feature","properties":{
"classe_2021":"Solo Rústico","categoria_2021":"Espaços Agrícolas",
"classificacao_e_qualificacao":"Solo Rústico - Espaços Agrícolas",
"dtcc":"1421","municipio":"OURÉM","codigo":21,"situacao_pdm":"Vigente"},
"geometry":{"type":"Polygon","coordinates":[[[-8.7,39.6],[-8.5,39.6],[-8.5,39.76],[-8.7,39.76],[-8.7,39.6]]]}}]}`

// TestPointOuremCRUSFallback: with live municipal WebSIG disabled, Ourém's
// dedicated adapter still falls back to CRUS for zoning plus the national
// constraint layers.
func TestPointOuremCRUSFallback(t *testing.T) {
	var seen []string
	// Live=false on purpose: the generic adapter must fetch live regardless,
	// because no bundled snapshot exists for it.
	eng := newEngineOpts(t, source.Options{Live: false, HTTP: stubClient(t, &seen, genericRoutes())})
	res, err := eng.Point(context.Background(), -8.60, 39.68) // Ourém
	if err != nil {
		t.Fatal(err)
	}
	if res.Municipality != "Ourém" || !res.Supported {
		t.Fatalf("expected supported Ourém, got %q supported=%v", res.Municipality, res.Supported)
	}
	if len(res.Zoning) != 1 || res.Zoning[0].Label != "Solo Rústico - Espaços Agrícolas" {
		t.Errorf("unexpected zoning: %+v", res.Zoning)
	}
	// The national constraint layers are evaluated for every municipality now.
	if len(res.Constraints) < 8 {
		t.Fatalf("expected the national constraint layers, got %d: %+v", len(res.Constraints), res.Constraints)
	}
	for _, typ := range []string{"RAN", "REN"} {
		c := findConstraint(res.Constraints, typ)
		if c == nil || c.Present || c.Unknown {
			t.Errorf("expected %s absent (data exists, point outside), got %+v", typ, c)
		}
	}
	if res.Regulation != nil {
		t.Error("Ourém adapter must not attach regulation until its regulamento is parsed")
	}
	if res.Confidence != model.ConfidenceMedium {
		t.Errorf("expected medium confidence, got %s", res.Confidence)
	}
	if hasNoteContaining(res.Notes, "no dedicated adapter") {
		t.Errorf("Ourém should now resolve through a dedicated adapter, got notes %v", res.Notes)
	}
	if !sawURLContaining(seen, "collections/crus/items", "dtcc=1421") {
		t.Errorf("expected CRUS fetch filtered by dtcc, got %v", seen)
	}
	// The registry attaches Ourém's special instruments (it is a PEPNSAC
	// municipality); nothing confirmed the point inside, so PointInside is nil.
	ins := findInstrumentContaining(res.Instruments, "Serras de Aire e Candeeiros")
	if ins == nil {
		t.Fatalf("expected PEPNSAC in Ourém's instruments, got %+v", res.Instruments)
	}
	if ins.PointInside != nil {
		t.Errorf("no layer confirmed the point inside PEPNSAC, got %v", *ins.PointInside)
	}
	if len(res.Sources) == 0 || res.Sources[0].Provenance != model.ProvenanceOfficialLive {
		t.Errorf("expected official-live source, got %+v", res.Sources)
	}
}

// TestNationalRENExclusion: a point inside an exclusion polygon is OUT of the
// REN. In the national data the delimitation multipolygon has the exclusion
// areas carved out geometrically, so such a point hits ONLY the exclusion
// feature (verified live against Tomar's city centre).
func TestNationalRENExclusion(t *testing.T) {
	routes := append([]stubRoute{
		{"srup_ren_areal/items?bbox", `{"numberMatched":1,"features":[
			{"type":"Feature","properties":{"tipologia":"Exclusões","designacao":"DELIMITAÇÃO DA REN DO CONCELHO - OURÉM"}}]}`},
	}, genericRoutes()...)
	eng := newEngineOpts(t, source.Options{Live: false, HTTP: stubClient(t, nil, routes)})
	res, err := eng.Point(context.Background(), -8.60, 39.68)
	if err != nil {
		t.Fatal(err)
	}
	ren := findConstraint(res.Constraints, "REN")
	if ren == nil || ren.Present || ren.Unknown {
		t.Fatalf("point in an exclusion area must not be REN-present, got %+v", ren)
	}
	if !strings.Contains(strings.ToLower(ren.Detail), "exclus") {
		t.Errorf("expected exclusion detail, got %q", ren.Detail)
	}
}

// TestNationalRENStraddle: a subject envelope hitting BOTH the delimitation and
// an exclusion straddles the boundary (a parcel, or a point on the line) —
// part of it is in the REN, so the honest answer is present, flagged partial.
func TestNationalRENStraddle(t *testing.T) {
	routes := append([]stubRoute{
		{"srup_ren_areal/items?bbox", `{"numberMatched":2,"features":[
			{"type":"Feature","properties":{"tipologia":"Reserva Ecológica Nacional","designacao":"DELIMITAÇÃO DA REN DO CONCELHO - OURÉM","serv_lei":"AVISO X/2024"}},
			{"type":"Feature","properties":{"tipologia":"Exclusões","designacao":"DELIMITAÇÃO DA REN DO CONCELHO - OURÉM"}}]}`},
	}, genericRoutes()...)
	eng := newEngineOpts(t, source.Options{Live: false, HTTP: stubClient(t, nil, routes)})
	res, err := eng.Polygon(context.Background(), squareAround(t, -8.60, 39.68, 0.001))
	if err != nil {
		t.Fatal(err)
	}
	ren := findConstraint(res.Constraints, "REN")
	if ren == nil || !ren.Present || ren.Unknown {
		t.Fatalf("a parcel straddling delimitation+exclusion is partially in REN, got %+v", ren)
	}
	if !strings.Contains(strings.ToLower(ren.Detail), "parcial") {
		t.Errorf("expected partial-coverage detail, got %q", ren.Detail)
	}
}

// TestNationalRANUnknown: a municipality absent from the national RAN dataset
// must answer "unknown", never "no".
func TestNationalRANUnknown(t *testing.T) {
	// No srup_ran existence route: the existence check returns numberMatched 0.
	routes := []stubRoute{
		{"collections/crus/items", ouremCRUS},
		{"srup_ren_areal/items?dtcc=1421&f=json&limit=1", `{"numberMatched":1,"features":[]}`},
	}
	eng := newEngineOpts(t, source.Options{Live: false, HTTP: stubClient(t, nil, routes)})
	res, err := eng.Point(context.Background(), -8.60, 39.68)
	if err != nil {
		t.Fatal(err)
	}
	ran := findConstraint(res.Constraints, "RAN")
	if ran == nil || !ran.Unknown || ran.Present {
		t.Fatalf("expected RAN unknown for a municipality missing from the dataset, got %+v", ran)
	}
	if ran.Note == "" {
		t.Error("an unknown answer must carry the data-gap note")
	}
	// A data gap is a reason to trust the overall result less.
	if res.Confidence != model.ConfidenceLow {
		t.Errorf("expected downgraded confidence on data gap, got %s", res.Confidence)
	}
}

// TestInstrumentsPOACBInside: at a point inside the bundled POACB area, the
// registry instrument for the Castelo do Bode plan is confirmed PointInside.
// TestPointFireHazardGeometry: the rural fire hazard chart is an srup
// geometry layer composed engine-side. Only the high classes count (the rest
// are filtered out), a hit carries the tipologia detail, and the Natura
// layers it brings must not duplicate the national probes (composeLayers
// dedupes by constraint string).
func TestPointFireHazardGeometry(t *testing.T) {
	const square = `"geometry":{"type":"Polygon","coordinates":[[[-8.7,39.6],[-8.5,39.6],[-8.5,39.76],[-8.7,39.76],[-8.7,39.6]]]}`
	const fireFC = `{"type":"FeatureCollection","features":[
		{"type":"Feature","properties":{"tipologia":"Muito Alta"},` + square + `},
		{"type":"Feature","properties":{"tipologia":"Baixa"},` + square + `}]}`
	var seen []string
	routes := append(genericRoutes(), stubRoute{"srup_perigosidade_inc_rural/items", fireFC})
	eng := newEngineOpts(t, source.Options{Live: false, HTTP: stubClient(t, &seen, routes)})
	res, err := eng.Point(context.Background(), -8.60, 39.68) // Ourém
	if err != nil {
		t.Fatal(err)
	}
	c := findConstraint(res.Constraints, "Perigosidade de incêndio rural")
	if c == nil || !c.Present {
		t.Fatalf("expected a present fire-hazard constraint, got %+v", c)
	}
	if c.Detail != "Muito Alta" {
		t.Errorf("expected the high-class tipologia as detail, got %q", c.Detail)
	}
	if !sawURLContaining(seen, "srup_perigosidade_inc_rural/items") {
		t.Errorf("expected a fire-hazard fetch, got %v", seen)
	}
	for _, typ := range []string{"Rede Natura 2000 (ZEC)", "Rede Natura 2000 (ZPE)", "Perigosidade de incêndio rural"} {
		n := 0
		for _, cc := range res.Constraints {
			if cc.Type == typ {
				n++
			}
		}
		if n != 1 {
			t.Errorf("expected exactly one %q constraint (dedup), got %d in %+v", typ, n, res.Constraints)
		}
	}
}

// TestPointFireHazardLowClassFiltered: a location covered only by low hazard
// classes must NOT flag the constraint — virtually the whole country carries
// some class, only alta/muito alta restrict building.
func TestPointFireHazardLowClassFiltered(t *testing.T) {
	const lowFC = `{"type":"FeatureCollection","features":[
		{"type":"Feature","properties":{"tipologia":"Baixa"},
		"geometry":{"type":"Polygon","coordinates":[[[-8.7,39.6],[-8.5,39.6],[-8.5,39.76],[-8.7,39.76],[-8.7,39.6]]]}}]}`
	routes := append(genericRoutes(), stubRoute{"srup_perigosidade_inc_rural/items", lowFC})
	eng := newEngineOpts(t, source.Options{Live: false, HTTP: stubClient(t, nil, routes)})
	res, err := eng.Point(context.Background(), -8.60, 39.68)
	if err != nil {
		t.Fatal(err)
	}
	c := findConstraint(res.Constraints, "Perigosidade de incêndio rural")
	if c == nil || c.Present || c.Unknown {
		t.Fatalf("expected fire hazard evaluated and absent (low class filtered), got %+v", c)
	}
}

func TestInstrumentsPOACBInside(t *testing.T) {
	res, err := newEngine(t).Point(shortCtx(t), -8.32522, 39.54334)
	if err != nil {
		t.Fatal(err)
	}
	poacb := findInstrumentContaining(res.Instruments, "POACB")
	if poacb == nil {
		t.Fatalf("expected POACB in Tomar's instruments, got %+v", res.Instruments)
	}
	if poacb.PointInside == nil || !*poacb.PointInside {
		t.Errorf("expected POACB confirmed at an interior point, got %+v", poacb.PointInside)
	}
}

func sawURLContaining(seen []string, subs ...string) bool {
	for _, u := range seen {
		ok := true
		for _, s := range subs {
			if !strings.Contains(u, s) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func findInstrumentContaining(ins []model.Instrument, sub string) *model.Instrument {
	for i := range ins {
		if strings.Contains(ins[i].Name, sub) {
			return &ins[i]
		}
	}
	return nil
}

// TestPointGenericOffline: with no network, a generic municipality still
// resolves and reports honestly that the zoning layer was unavailable.
func TestPointGenericOffline(t *testing.T) {
	res, err := newEngine(t).Point(shortCtx(t), -8.60, 39.68) // Ourém
	if err != nil {
		t.Fatal(err)
	}
	if res.Municipality != "Ourém" || !res.Supported {
		t.Fatalf("expected supported Ourém, got %q supported=%v", res.Municipality, res.Supported)
	}
	if len(res.Zoning) != 0 {
		t.Errorf("expected no zoning offline, got %+v", res.Zoning)
	}
	if !hasNoteContaining(res.Notes, "unavailable") {
		t.Errorf("expected layer-unavailable note, got %v", res.Notes)
	}
	if res.Confidence != model.ConfidenceLow {
		t.Errorf("expected low confidence, got %s", res.Confidence)
	}
}

// TestPointAutonomousRegions: island coordinates resolve to no municipality
// but the note says why.
func TestPointAutonomousRegions(t *testing.T) {
	cases := map[string][2]float64{
		"Madeira": {-16.92, 32.65}, // Funchal
		"Azores":  {-25.67, 37.74}, // Ponta Delgada
	}
	for region, c := range cases {
		res, err := newEngine(t).Point(shortCtx(t), c[0], c[1])
		if err != nil {
			t.Fatal(err)
		}
		if res.Supported || res.Municipality != "(undetermined)" {
			t.Errorf("%s: expected undetermined, got %q supported=%v", region, res.Municipality, res.Supported)
		}
		if !hasNoteContaining(res.Notes, region) {
			t.Errorf("%s: expected region note, got %v", region, res.Notes)
		}
	}
}

func hasNoteContaining(notes []string, sub string) bool {
	for _, n := range notes {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

// squareAround builds a small square Polygon centred on (lon,lat).
func squareAround(t *testing.T, lon, lat, half float64) geom.Geometry {
	t.Helper()
	gj := fmt.Sprintf(`{"type":"Polygon","coordinates":[[[%g,%g],[%g,%g],[%g,%g],[%g,%g],[%g,%g]]]}`,
		lon-half, lat-half, lon+half, lat-half, lon+half, lat+half, lon-half, lat+half, lon-half, lat-half)
	g, err := geom.UnmarshalGeoJSON([]byte(gj), geom.NoValidate{})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// TestPolygonOuremCRUSFallback: the parcel path uses CRUS geometry when live
// municipal WebSIG layers are disabled.
func TestPolygonOuremCRUSFallback(t *testing.T) {
	eng := newEngineOpts(t, source.Options{Live: false, HTTP: stubClient(t, nil, genericRoutes())})
	g := squareAround(t, -8.60, 39.68, 0.001) // inside Ourém and the stub zoning polygon
	res, err := eng.Polygon(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}
	if res.Municipality != "Ourém" || !res.Supported {
		t.Fatalf("expected supported Ourém, got %q supported=%v", res.Municipality, res.Supported)
	}
	if len(res.Zoning) != 1 || res.Zoning[0].Label != "Solo Rústico - Espaços Agrícolas" {
		t.Fatalf("unexpected zoning: %+v", res.Zoning)
	}
	if res.Zoning[0].Percent < 99 || res.Zoning[0].Percent > 100.5 {
		t.Errorf("parcel fully inside the zoning polygon should be ~100%%, got %.1f", res.Zoning[0].Percent)
	}
	ran := findConstraint(res.Constraints, "RAN")
	if ran == nil || ran.Present || ran.Unknown {
		t.Fatalf("expected RAN absent, got %+v", ran)
	}
	if !strings.Contains(ran.Note, "envelope") {
		t.Errorf("polygon probes must carry the envelope-approximation note, got %q", ran.Note)
	}
	if hasNoteContaining(res.Notes, "no dedicated adapter") {
		t.Errorf("Ourém should now resolve through a dedicated adapter, got notes %v", res.Notes)
	}
	if res.Confidence != model.ConfidenceMedium {
		t.Errorf("expected medium confidence, got %s", res.Confidence)
	}
}

// TestPolygonAutonomousRegion: a parcel in Madeira resolves to no municipality
// but keeps its area and explains why.
func TestPolygonAutonomousRegion(t *testing.T) {
	res, err := newEngine(t).Polygon(shortCtx(t), squareAround(t, -16.92, 32.65, 0.001))
	if err != nil {
		t.Fatal(err)
	}
	if res.Supported || res.Municipality != "(undetermined)" {
		t.Fatalf("expected undetermined, got %q supported=%v", res.Municipality, res.Supported)
	}
	if res.AnalysedAreaM2 <= 0 {
		t.Error("undetermined polygon should still report its area")
	}
	if !hasNoteContaining(res.Notes, "Madeira") {
		t.Errorf("expected Madeira note, got %v", res.Notes)
	}
}

// TestMapsOuremCRUSFallback: the map builders still render from CRUS when live
// municipal WebSIG layers are disabled.
func TestMapsOuremCRUSFallback(t *testing.T) {
	eng := newEngineOpts(t, source.Options{Live: false, HTTP: jsonClient(t, ouremCRUS, nil)})
	pm := eng.PointMap(context.Background(), -8.60, 39.68)
	if pm == nil {
		t.Fatal("PointMap should render for Ourém")
	}
	if !hasMapLayer(pm.Layers, "boundary") || !hasMapLayer(pm.Layers, "zoning") {
		t.Errorf("expected boundary+zoning map layers, got %+v", layerIDs(pm.Layers))
	}
	gm := eng.PolygonMap(context.Background(), squareAround(t, -8.60, 39.68, 0.001))
	if gm == nil {
		t.Fatal("PolygonMap should render for a generic municipality")
	}
	if !hasMapLayer(gm.Layers, "zoning") {
		t.Errorf("expected zoning map layer, got %+v", layerIDs(gm.Layers))
	}
}

func TestPointStreamReusesZoningForMap(t *testing.T) {
	var seen []string
	eng := newEngineOpts(t, source.Options{Live: false, HTTP: stubClient(t, &seen, genericRoutes())})
	var mu sync.Mutex
	var maps []query.MapEvent
	var zoningLayers []query.LayerEvent
	_, err := eng.PointStream(context.Background(), -8.60, 39.68, func(v any) {
		switch ev := v.(type) {
		case query.MapEvent:
			mu.Lock()
			maps = append(maps, ev)
			mu.Unlock()
		case query.LayerEvent:
			if ev.ID == "ordenamento" {
				mu.Lock()
				zoningLayers = append(zoningLayers, ev)
				mu.Unlock()
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(maps) != 1 || !strings.Contains(maps[0].HTML, `data-role="zoning"`) {
		t.Fatalf("expected one streamed locator map with zoning, got %+v", maps)
	}
	if len(zoningLayers) != 1 || zoningLayers[0].ZoningGeoJSON == nil {
		t.Fatalf("expected one zoning layer event with GeoJSON, got %+v", zoningLayers)
	}
	crusRequests := 0
	for _, u := range seen {
		if strings.Contains(u, "collections/crus/items") {
			crusRequests++
		}
	}
	if crusRequests != 1 {
		t.Fatalf("expected the streamed query and map to share one CRUS request, got %d: %v", crusRequests, seen)
	}
}

func hasMapLayer(layers []mapview.Layer, id string) bool {
	for _, l := range layers {
		if l.ID == id {
			return true
		}
	}
	return false
}

func layerIDs(layers []mapview.Layer) []string {
	var ids []string
	for _, l := range layers {
		ids = append(ids, l.ID)
	}
	return ids
}

func TestPolygonBreakdown(t *testing.T) {
	g, err := spatial.LoadInputGeometry("../../testdata/parcel-large.geojson")
	if err != nil {
		t.Fatal(err)
	}
	res, err := newEngine(t).Polygon(shortCtx(t), g)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Supported || res.Municipality != "Tomar" {
		t.Fatalf("expected Tomar, got %q", res.Municipality)
	}
	if res.AnalysedAreaM2 < 1000 {
		t.Fatalf("analysed area too small: %v", res.AnalysedAreaM2)
	}
	// Zoning must be sorted descending by area and percentages within [0,100].
	var prev = 1e18
	var sum float64
	for _, z := range res.Zoning {
		if z.AreaM2 > prev {
			t.Error("zoning not sorted descending by area")
		}
		prev = z.AreaM2
		sum += z.Percent
		if z.Percent < 0 || z.Percent > 100.5 {
			t.Errorf("percent out of range: %v", z.Percent)
		}
	}
	if sum > 100.5 {
		t.Errorf("zoning percentages sum to %.1f%% (>100)", sum)
	}
}

func findConstraint(cs []model.ConstraintHit, typ string) *model.ConstraintHit {
	for i := range cs {
		if cs[i].Type == typ {
			return &cs[i]
		}
	}
	return nil
}
