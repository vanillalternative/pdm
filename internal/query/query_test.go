package query_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

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

const emptyFC = `{"type":"FeatureCollection","features":[]}`

// routedClient answers each request with the body whose key is a substring of
// the URL (empty FeatureCollection otherwise), recording seen URLs.
func routedClient(t *testing.T, routes map[string]string, sawURLs *[]string) *http.Client {
	t.Helper()
	return &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		u := r.URL.String()
		if sawURLs != nil {
			*sawURLs = append(*sawURLs, u)
		}
		body := emptyFC
		for sub, b := range routes {
			if strings.Contains(u, sub) {
				body = b
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

func TestPointTomarCentre(t *testing.T) {
	res, err := newEngine(t).Point(context.Background(), -8.41, 39.60)
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
	in, err := eng.Point(context.Background(), -8.32522, 39.54334)
	if err != nil {
		t.Fatal(err)
	}
	if c := findConstraint(in.Constraints, "Albufeira Castelo de Bode"); c == nil || !c.Present {
		t.Errorf("expected POACB present at interior point, got %+v", c)
	}
	// Just outside it (~500 m).
	out, err := eng.Point(context.Background(), -8.27746, 39.58687)
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

// TestPointGenericMunicipality: a municipality without a dedicated adapter is
// served by the generic adapter — zoning from CRUS plus the national SRUP
// constraints, all fetched live, capped at low confidence, and honestly
// annotated.
func TestPointGenericMunicipality(t *testing.T) {
	var sawURLs []string
	// Live=false on purpose: the generic adapter must fetch live regardless,
	// because no bundled snapshot exists for it.
	eng := newEngineOpts(t, source.Options{Live: false, HTTP: routedClient(t, map[string]string{"collections/crus/": ouremCRUS}, &sawURLs)})
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
	// National SRUP constraints are evaluated (absent here — empty stub).
	if len(res.Constraints) != 3 {
		t.Fatalf("expected the 3 national SRUP constraints, got %+v", res.Constraints)
	}
	for _, c := range res.Constraints {
		if c.Present {
			t.Errorf("constraint %s should be absent with empty stub data", c.Type)
		}
	}
	if res.Regulation != nil {
		t.Error("generic adapter must not attach regulation")
	}
	if res.Confidence != model.ConfidenceLow {
		t.Errorf("generic result should be low confidence, got %s", res.Confidence)
	}
	if !hasNoteContaining(res.Notes, "not yet integrated") {
		t.Errorf("expected zoning-only caveat note, got %v", res.Notes)
	}
	joined := strings.Join(sawURLs, "\n")
	if !strings.Contains(joined, "collections/crus/items") || !strings.Contains(joined, "dtcc=1421") {
		t.Errorf("expected CRUS fetch filtered by dtcc, got %s", joined)
	}
	for _, coll := range []string{"srup_zpe", "srup_zec", "srup_perigosidade_inc_rural"} {
		if !strings.Contains(joined, coll) {
			t.Errorf("expected a %s fetch, got %s", coll, joined)
		}
	}
	if len(res.Sources) == 0 || res.Sources[0].Provenance != model.ProvenanceOfficialLive {
		t.Errorf("expected official-live source, got %+v", res.Sources)
	}
}

// ouremZEC is a canned ZEC polygon covering the Ourém test point; ouremFire is
// a fire-hazard response mixing a low class (must be ignored) and a high class
// polygon that does NOT cover the test point.
const ouremZEC = `{"type":"FeatureCollection","features":[{"type":"Feature","properties":{
"designacao":"SICÓ/ALVAIÁZERE","servidao":"REDE NATURA 2000 - ZONAS ESPECIAIS DE CONSERVAÇÃO"},
"geometry":{"type":"Polygon","coordinates":[[[-8.7,39.6],[-8.5,39.6],[-8.5,39.76],[-8.7,39.76],[-8.7,39.6]]]}}]}`

const ouremFire = `{"type":"FeatureCollection","features":[
{"type":"Feature","properties":{"tipologia":"Baixa"},
"geometry":{"type":"Polygon","coordinates":[[[-8.7,39.6],[-8.5,39.6],[-8.5,39.76],[-8.7,39.76],[-8.7,39.6]]]}},
{"type":"Feature","properties":{"tipologia":"Muito Alta"},
"geometry":{"type":"Polygon","coordinates":[[[-8.62,39.70],[-8.61,39.70],[-8.61,39.71],[-8.62,39.71],[-8.62,39.70]]]}}]}`

// TestPointGenericNationalConstraints: the SRUP layers mark presence and carry
// details, and the fire-hazard layer ignores the non-restrictive classes.
func TestPointGenericNationalConstraints(t *testing.T) {
	eng := newEngineOpts(t, source.Options{Live: false, HTTP: routedClient(t, map[string]string{
		"collections/crus/":           ouremCRUS,
		"srup_zec":                    ouremZEC,
		"srup_perigosidade_inc_rural": ouremFire,
	}, nil)})
	res, err := eng.Point(context.Background(), -8.60, 39.68) // Ourém
	if err != nil {
		t.Fatal(err)
	}
	zec := findConstraint(res.Constraints, "Natura 2000 — ZEC")
	if zec == nil || !zec.Present || zec.Detail != "SICÓ/ALVAIÁZERE" {
		t.Errorf("expected ZEC present with designação, got %+v", zec)
	}
	if zpe := findConstraint(res.Constraints, "Natura 2000 — ZPE"); zpe == nil || zpe.Present {
		t.Errorf("expected ZPE absent (empty stub), got %+v", zpe)
	}
	// The point sits inside the "Baixa" polygon (filtered out) and outside the
	// "Muito Alta" one — the constraint must be absent.
	if fire := findConstraint(res.Constraints, "Perigosidade de incêndio rural"); fire == nil || fire.Present {
		t.Errorf("expected fire hazard absent (only high classes count), got %+v", fire)
	}
}

// TestPointGenericOffline: with no network, a generic municipality still
// resolves and reports honestly that the zoning layer was unavailable.
func TestPointGenericOffline(t *testing.T) {
	res, err := newEngine(t).Point(context.Background(), -8.60, 39.68) // Ourém
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
		res, err := newEngine(t).Point(context.Background(), c[0], c[1])
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

// TestPolygonGenericMunicipality: the parcel path through the generic adapter —
// zoning breakdown from a live CRUS fetch, national constraints evaluated
// (absent with empty stubs), caveat note.
func TestPolygonGenericMunicipality(t *testing.T) {
	eng := newEngineOpts(t, source.Options{Live: false, HTTP: routedClient(t, map[string]string{"collections/crus/": ouremCRUS}, nil)})
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
	if len(res.Constraints) != 3 {
		t.Fatalf("expected the 3 national SRUP constraints, got %+v", res.Constraints)
	}
	for _, c := range res.Constraints {
		if c.Present {
			t.Errorf("constraint %s should be absent with empty stub data", c.Type)
		}
	}
	if !hasNoteContaining(res.Notes, "not yet integrated") {
		t.Errorf("expected zoning-only caveat note, got %v", res.Notes)
	}
	if res.Confidence != model.ConfidenceLow {
		t.Errorf("expected low confidence, got %s", res.Confidence)
	}
}

// TestPolygonAutonomousRegion: a parcel in Madeira resolves to no municipality
// but keeps its area and explains why.
func TestPolygonAutonomousRegion(t *testing.T) {
	res, err := newEngine(t).Polygon(context.Background(), squareAround(t, -16.92, 32.65, 0.001))
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

// TestMapsGenericMunicipality: the map builders must render for generic
// municipalities (they used to return nil for anything but Tomar).
func TestMapsGenericMunicipality(t *testing.T) {
	eng := newEngineOpts(t, source.Options{Live: false, HTTP: routedClient(t, map[string]string{"collections/crus/": ouremCRUS}, nil)})
	pm := eng.PointMap(context.Background(), -8.60, 39.68)
	if pm == nil {
		t.Fatal("PointMap should render for a generic municipality")
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
	res, err := newEngine(t).Polygon(context.Background(), g)
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
