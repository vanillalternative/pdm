package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/bernardosimoes/pdm/internal/cli"
	"github.com/bernardosimoes/pdm/internal/server"
)

// TestMain pins the environment so the suite is hermetic, mirroring the cli
// suite: a stale shell export must not change what the tests exercise.
func TestMain(m *testing.M) {
	for _, v := range []string{"PDM_TRUTH_API", "PDM_FETCH_TIMEOUT_SECONDS", "PDM_FETCH_MAX_ATTEMPTS", "PDM_LISTEN"} {
		os.Unsetenv(v)
	}
	os.Exit(m.Run())
}

func newTestServer(t *testing.T, cfg server.Config) *httptest.Server {
	t.Helper()
	if cfg.CacheDir == "" {
		cfg.CacheDir = t.TempDir()
	}
	s, err := server.New(cfg)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// pinFastFetch keeps offline probe layers from eating the full retry budget.
func pinFastFetch(t *testing.T) {
	t.Helper()
	t.Setenv("PDM_FETCH_TIMEOUT_SECONDS", "2")
	t.Setenv("PDM_FETCH_MAX_ATTEMPTS", "1")
}

func TestHealthAndVersion(t *testing.T) {
	ts := newTestServer(t, server.Config{Version: "test-1"})

	res, err := http.Get(ts.URL + "/healthz")
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("healthz: %v status=%v", err, res)
	}
	res.Body.Close()

	res, err = http.Get(ts.URL + "/v1/version")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var v struct {
		Version string `json:"version"`
		BinHash string `json:"binhash"`
	}
	if err := json.NewDecoder(res.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	if v.Version != "test-1" {
		t.Errorf("version = %q, want test-1", v.Version)
	}
}

// TestMunicipalitiesParity: /v1/municipalities is byte-identical to the CLI
// command's stdout — the wire contract web/server.js parses at boot.
func TestMunicipalitiesParity(t *testing.T) {
	ts := newTestServer(t, server.Config{})

	res, err := http.Get(ts.URL + "/v1/municipalities")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	got, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := cli.Run([]string{"municipalities"}, &out, &errb); code != 0 {
		t.Fatalf("cli exit %d, stderr=%q", code, errb.String())
	}
	if !bytes.Equal(got, out.Bytes()) {
		t.Fatalf("daemon municipalities differs from CLI output (daemon %d bytes, cli %d bytes)", len(got), out.Len())
	}
}

// TestReportNDJSONEventOrder guards the streaming contract over HTTP, the
// daemon counterpart of the CLI's TestRunReportNDJSONEventOrder: meta first,
// result terminal, every line standalone JSON.
func TestReportNDJSONEventOrder(t *testing.T) {
	pinFastFetch(t)
	ts := newTestServer(t, server.Config{})

	res, err := http.Get(ts.URL + "/v1/report?lat=39.60&lon=-8.41&no_cache=1")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/x-ndjson") {
		t.Errorf("content-type = %q", ct)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least meta+layer+result events, got %d lines", len(lines))
	}
	events := make([]string, len(lines))
	for i, l := range lines {
		var ev struct {
			Event string `json:"event"`
		}
		if err := json.Unmarshal([]byte(l), &ev); err != nil {
			t.Fatalf("line %d is not JSON: %v\n%s", i, err, l)
		}
		events[i] = ev.Event
	}
	if events[0] != "meta" {
		t.Errorf("first event = %q, want meta", events[0])
	}
	if events[len(events)-1] != "result" {
		t.Errorf("terminal event = %q, want result", events[len(events)-1])
	}
	for _, e := range events[1 : len(events)-1] {
		if e == "meta" || e == "result" {
			t.Errorf("unexpected mid-stream %q event in %v", e, events)
		}
	}
}

// TestReportBufferedJSON: non-streaming formats answer 200 with the rendered
// document, like `pdm report --format json`.
func TestReportBufferedJSON(t *testing.T) {
	pinFastFetch(t)
	ts := newTestServer(t, server.Config{})

	res, err := http.Get(ts.URL + "/v1/report?lat=39.60&lon=-8.41&no_cache=1&format=json")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status = %d", res.StatusCode)
	}
	var doc struct {
		Municipality string `json:"municipality"`
	}
	if err := json.NewDecoder(res.Body).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	if doc.Municipality != "Tomar" {
		t.Errorf("municipality = %q, want Tomar", doc.Municipality)
	}
}

// TestPolygonReportPOST: a GeoJSON body replaces the CLI's temp-file input.
func TestPolygonReportPOST(t *testing.T) {
	pinFastFetch(t)
	ts := newTestServer(t, server.Config{})

	// A small parcel inside Tomar (bundled data, offline).
	geojson := `{"type":"Polygon","coordinates":[[[-8.412,39.599],[-8.410,39.599],[-8.410,39.601],[-8.412,39.601],[-8.412,39.599]]]}`
	res, err := http.Post(ts.URL+"/v1/report?no_cache=1", "application/geo+json", strings.NewReader(geojson))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("status = %d body=%s", res.StatusCode, body)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	var meta struct {
		Event string `json:"event"`
		Kind  string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Event != "meta" || meta.Kind != "polygon" {
		t.Errorf("first event = %+v, want meta/polygon", meta)
	}
	var last struct {
		Event string `json:"event"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatal(err)
	}
	if last.Event != "result" {
		t.Errorf("terminal event = %q, want result", last.Event)
	}
}

// TestValidationContract: bad input answers 422 {"ok":false,...} before any
// stream bytes — the daemon counterpart of pdm's pre-stdout arg errors.
func TestValidationContract(t *testing.T) {
	ts := newTestServer(t, server.Config{})

	cases := []struct {
		name string
		do   func() (*http.Response, error)
	}{
		{"missing coords", func() (*http.Response, error) {
			return http.Get(ts.URL + "/v1/report")
		}},
		{"swapped coords", func() (*http.Response, error) {
			return http.Get(ts.URL + "/v1/report?lat=-8.41&lon=39.60")
		}},
		{"bad format", func() (*http.Response, error) {
			return http.Get(ts.URL + "/v1/report?lat=39.60&lon=-8.41&format=yaml")
		}},
		{"bad truth", func() (*http.Response, error) {
			return http.Get(ts.URL + "/v1/report?lat=39.60&lon=-8.41&truth=maybe")
		}},
		{"bad geojson body", func() (*http.Response, error) {
			return http.Post(ts.URL+"/v1/report", "application/json", strings.NewReader("{nope"))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := tc.do()
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			if res.StatusCode != 422 {
				t.Fatalf("status = %d, want 422", res.StatusCode)
			}
			var e struct {
				OK    bool   `json:"ok"`
				Error string `json:"error"`
			}
			if err := json.NewDecoder(res.Body).Decode(&e); err != nil {
				t.Fatal(err)
			}
			if e.OK || e.Error == "" {
				t.Errorf("body = %+v, want ok=false with an error", e)
			}
		})
	}
}

// TestTruthToggle: truth=off must never contact the configured mirror, and the
// per-request toggle must not leak between requests (the daemon reuses one
// base options value).
func TestTruthToggle(t *testing.T) {
	pinFastFetch(t)
	var hits atomic.Int64
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.NotFound(w, r)
	}))
	defer mirror.Close()

	ts := newTestServer(t, server.Config{TruthAPI: mirror.URL})

	// Lisboa is a generic municipality: point zoning consults the mirror first.
	get := func(truth string) {
		t.Helper()
		res, err := http.Get(ts.URL + "/v1/report?lat=38.72&lon=-9.14&no_cache=1&truth=" + truth)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("status = %d", res.StatusCode)
		}
		_, _ = io.Copy(io.Discard, res.Body)
	}

	get("off")
	if n := hits.Load(); n != 0 {
		t.Fatalf("truth=off contacted the mirror %d times", n)
	}
	get("on")
	if n := hits.Load(); n == 0 {
		t.Fatalf("truth=on never contacted the mirror")
	}
	get("off")
	if n := hits.Load(); n > 2 {
		t.Fatalf("truth=off after truth=on leaked mirror calls (total %d)", n)
	}
}

// TestConcurrentQueries certifies the shared resolvers/cache as safe for
// concurrent use — run with -race, this is the gate for the daemon's whole
// one-engine-state-many-requests design.
func TestConcurrentQueries(t *testing.T) {
	pinFastFetch(t)
	ts := newTestServer(t, server.Config{})

	const n = 12
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Vary the point slightly so requests don't trivially serialize.
			url := fmt.Sprintf("%s/v1/report?lat=%.4f&lon=%.4f&no_cache=1", ts.URL, 39.60+float64(i%3)*0.001, -8.41)
			res, err := http.Get(url)
			if err != nil {
				errs <- err
				return
			}
			defer res.Body.Close()
			if res.StatusCode != 200 {
				errs <- fmt.Errorf("status %d", res.StatusCode)
				return
			}
			body, err := io.ReadAll(res.Body)
			if err != nil {
				errs <- err
				return
			}
			if !strings.Contains(string(body), `"event":"result"`) {
				errs <- fmt.Errorf("no terminal result event")
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
