package source

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bernardosimoes/pdm/internal/cache"
	"github.com/bernardosimoes/pdm/internal/model"
	"github.com/bernardosimoes/pdm/internal/spatial"
)

// truthFC is a mirror response whose polygon covers the test point (-8.3, 39.7).
const truthFC = `{"type":"FeatureCollection","features":[{"type":"Feature","properties":{
"class":"Solo rústico","subclass":"Espaços agrícolas","label":"Solo rústico — Espaços agrícolas",
"raw_code":"21","color":"#c9912e","layer_id":"ordenamento","muni_code":"1411","recorded_at":"2026-08-01T10:00:00Z"},
"geometry":{"type":"Polygon","coordinates":[[[-8.4,39.6],[-8.2,39.6],[-8.2,39.8],[-8.4,39.8],[-8.4,39.6]]]}}],
"pdms":{"count":1,"next_after":null,"recorded_from":["DGT/SNIT — CRUS (ordenamento do solo)"],"updated_at":"2026-08-02T12:30:00Z"}}`

// truthGapFC is a mirror response whose polygon does NOT cover the test point.
const truthGapFC = `{"type":"FeatureCollection","features":[{"type":"Feature","properties":{
"class":"Solo urbano","label":"Solo urbano"},
"geometry":{"type":"Polygon","coordinates":[[[-8.1,39.6],[-8.0,39.6],[-8.0,39.7],[-8.1,39.7],[-8.1,39.6]]]}}],
"pdms":{"count":1,"next_after":null,"recorded_from":[],"updated_at":"2026-08-02T12:30:00Z"}}`

func truthOpts(subjectLon, subjectLat float64) Options {
	return Options{Subject: spatial.Point(subjectLon, subjectLat)}
}

func truthCfg(baseURL string) TruthConfig {
	return TruthConfig{
		BaseURL: baseURL,
		Code:    "1411",
		Meta:    model.Source{Name: "pdms — espelho de zonamento", Layer: "ordenamento"},
	}
}

func TestTruthHit(t *testing.T) {
	var sawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawQuery = r.URL.Path + "?" + r.URL.RawQuery
		fmt.Fprint(w, truthFC)
	}))
	defer srv.Close()

	loaded, err := Truth(truthCfg(srv.URL), truthOpts(-8.3, 39.7))(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/api/truth/zoning?", "code=1411", "lat=39.7", "lon=-8.3"} {
		if !strings.Contains(sawQuery, want) {
			t.Errorf("request %q missing %q", sawQuery, want)
		}
	}
	if len(loaded.Features) != 1 {
		t.Fatalf("expected 1 feature, got %d", len(loaded.Features))
	}
	if !FromTruthMirror(loaded.Features[0]) {
		t.Error("mirror feature must carry the marker prop")
	}
	if loaded.Source.Provenance != model.ProvenanceRecordedMirror {
		t.Errorf("provenance = %q, want recorded-mirror", loaded.Source.Provenance)
	}
	wantName := "pdms — espelho de zonamento (orig.: DGT/SNIT — CRUS (ordenamento do solo))"
	if loaded.Source.Name != wantName {
		t.Errorf("source name = %q, want %q", loaded.Source.Name, wantName)
	}
	wantAt := time.Date(2026, 8, 2, 12, 30, 0, 0, time.UTC)
	if loaded.Source.RetrievedAt == nil || !loaded.Source.RetrievedAt.Equal(wantAt) {
		t.Errorf("retrieved_at = %v, want envelope updated_at %v", loaded.Source.RetrievedAt, wantAt)
	}
}

func TestTruthGapMiss(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, truthGapFC)
	}))
	defer srv.Close()

	_, err := Truth(truthCfg(srv.URL), truthOpts(-8.3, 39.7))(context.Background())
	if !errors.Is(err, ErrTruthMiss) {
		t.Fatalf("a feature set not covering the point must be ErrTruthMiss, got %v", err)
	}
}

func TestTruthEmptyMiss(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"type":"FeatureCollection","features":[],"pdms":{"count":0}}`)
	}))
	defer srv.Close()

	_, err := Truth(truthCfg(srv.URL), truthOpts(-8.3, 39.7))(context.Background())
	if !errors.Is(err, ErrTruthMiss) {
		t.Fatalf("an empty FeatureCollection must be ErrTruthMiss, got %v", err)
	}
}

func TestTruthNoSubjectMiss(t *testing.T) {
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		fmt.Fprint(w, truthFC)
	}))
	defer srv.Close()

	_, err := Truth(truthCfg(srv.URL), Options{})(context.Background())
	if !errors.Is(err, ErrTruthMiss) {
		t.Fatalf("a zero Subject must be ErrTruthMiss, got %v", err)
	}
	if n := atomic.LoadInt32(&requests); n != 0 {
		t.Errorf("no request may be issued without a subject, saw %d", n)
	}
}

func TestTruthServerDownFastFail(t *testing.T) {
	restore := truthTimeout
	truthTimeout = 300 * time.Millisecond
	defer func() { truthTimeout = restore }()

	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		<-r.Context().Done() // hang until the client gives up
	}))
	defer srv.Close()

	start := time.Now()
	_, err := Truth(truthCfg(srv.URL), truthOpts(-8.3, 39.7))(context.Background())
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected an error against a hung mirror")
	}
	if n := atomic.LoadInt32(&requests); n != 1 {
		t.Fatalf("a hung mirror gets exactly one attempt, saw %d", n)
	}
	// Generous bound: only trips if the production timeout were absent (the
	// handler hangs until the deadline fires, so no timeout means no return).
	if elapsed > 5*time.Second {
		t.Fatalf("mirror failure took %v; truthTimeout must bound the attempt", elapsed)
	}
}

// TestTruthBaseURLRobust: a base URL with a trailing slash or a stray query
// string must still produce a request against the API path.
func TestTruthBaseURLRobust(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		mu.Unlock()
		fmt.Fprint(w, truthFC)
	}))
	defer srv.Close()

	for _, base := range []string{srv.URL + "/", srv.URL + "/?ref=1", srv.URL + "#frag"} {
		if _, err := Truth(truthCfg(base), truthOpts(-8.3, 39.7))(context.Background()); err != nil {
			t.Fatalf("base %q: %v", base, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 3 {
		t.Fatalf("expected 3 requests, got %v", paths)
	}
	for _, p := range paths {
		if !strings.HasPrefix(p, "/api/truth/zoning?") || !strings.Contains(p, "code=1411") || strings.Contains(p, "ref=1") {
			t.Errorf("request %q must target the API path with the query params only", p)
		}
	}
}

func TestTruthMalformedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html>maintenance</html>`)
	}))
	defer srv.Close()

	if _, err := Truth(truthCfg(srv.URL), truthOpts(-8.3, 39.7))(context.Background()); err == nil {
		t.Fatal("expected an error on a non-GeoJSON body")
	}
}

func TestTruthServerErrorSingleAttempt(t *testing.T) {
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := Truth(truthCfg(srv.URL), truthOpts(-8.3, 39.7))(context.Background()); err == nil {
		t.Fatal("expected an error on 500")
	}
	if n := atomic.LoadInt32(&requests); n != 1 {
		t.Fatalf("the mirror gets exactly one attempt, saw %d", n)
	}
}

func TestTruthNeverTouchesDiskCache(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, truthFC)
	}))
	defer srv.Close()

	dir := t.TempDir()
	c, err := cache.New(cache.Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	opts := truthOpts(-8.3, 39.7)
	opts.Cache = c
	if _, err := Truth(truthCfg(srv.URL), opts)(context.Background()); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a mirror hit must not write the disk cache, found %d entries", len(entries))
	}
}

func TestTruthGapFallsBackToBundled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, truthGapFC)
	}))
	defer srv.Close()

	const bundledFC = `{"type":"FeatureCollection","features":[{"type":"Feature","properties":{"classe":"Solo rústico"},
"geometry":{"type":"Polygon","coordinates":[[[-8.4,39.6],[-8.2,39.6],[-8.2,39.8],[-8.4,39.8],[-8.4,39.6]]]}}]}`
	loader := Fallback(
		Truth(truthCfg(srv.URL), truthOpts(-8.3, 39.7)),
		Bundled([]byte(bundledFC), model.Source{Name: "bundled zoning", Provenance: model.ProvenanceBundled}),
	)
	loaded, err := loader(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Source.Name != "bundled zoning" || len(loaded.Features) != 1 {
		t.Fatalf("expected the bundled answer after a mirror gap, got %+v", loaded.Source)
	}
}
