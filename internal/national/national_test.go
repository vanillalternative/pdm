package national

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/bernardosimoes/pdm/internal/source"
)

// snapshotStub serves /api/truth/constraints from a per-dataset table of
// (feature props, distance) rows, filtered by the requested distance_m — the
// contract the web store implements over PostGIS.
type snapshotRow struct {
	props map[string]any
	dist  float64
}

func snapshotStub(t *testing.T, data map[string][]snapshotRow) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/truth/constraints" {
			http.NotFound(w, r)
			return
		}
		ds := r.URL.Query().Get("dataset")
		rows, ok := data[ds]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "dataset not loaded"})
			return
		}
		maxD := 0.0
		if v := r.URL.Query().Get("distance_m"); v != "" {
			maxD, _ = strconv.ParseFloat(v, 64)
		}
		feats := []map[string]any{}
		for _, row := range rows {
			if row.dist <= maxD {
				feats = append(feats, map[string]any{"properties": row.props, "distance_m": row.dist})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pdms":     map[string]any{"dataset": ds, "harvested_at": "2026-08-20T16:45:00Z", "feature_count": len(rows)},
			"features": feats,
		})
	}))
}

func probeSubject() source.BBox {
	return source.BBox{MinLon: -8.325, MinLat: 39.535, MaxLon: -8.315, MaxLat: 39.545}
}

func TestAlbufeiraProbeSnapshotOnWater(t *testing.T) {
	srv := snapshotStub(t, map[string][]snapshotRow{
		"albufeiras": {{props: map[string]any{"nome": "Castelo de Bode", "classifica": "Protegida"}, dist: 0}},
	})
	defer srv.Close()
	p := albufeiraProbe(source.Options{SnapshotAPI: srv.URL})
	got, err := p(context.Background(), probeSubject())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Present || !strings.Contains(got.Detail, "sobre o plano de água") || !strings.Contains(got.Detail, "Castelo de Bode") {
		t.Fatalf("unexpected probed: %+v", got)
	}
	if got.Source.Provenance != "official-snapshot" {
		t.Errorf("provenance = %q, want official-snapshot", got.Source.Provenance)
	}
	if got.Source.RetrievedAt == nil || got.Source.RetrievedAt.Year() != 2026 {
		t.Errorf("retrieved_at should carry the harvest date, got %v", got.Source.RetrievedAt)
	}
}

func TestAlbufeiraProbeSnapshotBelt500(t *testing.T) {
	srv := snapshotStub(t, map[string][]snapshotRow{
		"albufeiras": {{props: map[string]any{"nome": "Castelo de Bode", "classifica": "Protegida"}, dist: 320}},
	})
	defer srv.Close()
	p := albufeiraProbe(source.Options{SnapshotAPI: srv.URL})
	got, err := p(context.Background(), probeSubject())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Present || !strings.Contains(got.Detail, "500 m") {
		t.Fatalf("expected the 500 m belt, got: %+v", got)
	}
}

// TestAlbufeiraProbeAlquevaFilter: only Alqueva gets the 1000 m belt — another
// reservoir at 800 m must NOT match (the snapshot path filters client-side
// where the live path used a Where clause).
func TestAlbufeiraProbeAlquevaFilter(t *testing.T) {
	t.Run("alqueva matches", func(t *testing.T) {
		srv := snapshotStub(t, map[string][]snapshotRow{
			"albufeiras": {{props: map[string]any{"nome": "Alqueva"}, dist: 800}},
		})
		defer srv.Close()
		p := albufeiraProbe(source.Options{SnapshotAPI: srv.URL})
		got, err := p(context.Background(), probeSubject())
		if err != nil {
			t.Fatal(err)
		}
		if !got.Present || !strings.Contains(got.Detail, "1000 m") {
			t.Fatalf("expected the Alqueva 1000 m belt, got: %+v", got)
		}
	})
	t.Run("other reservoir does not", func(t *testing.T) {
		srv := snapshotStub(t, map[string][]snapshotRow{
			"albufeiras": {{props: map[string]any{"nome": "Castelo de Bode"}, dist: 800}},
		})
		defer srv.Close()
		p := albufeiraProbe(source.Options{SnapshotAPI: srv.URL})
		got, err := p(context.Background(), probeSubject())
		if err != nil {
			t.Fatal(err)
		}
		if got.Present {
			t.Fatalf("800 m from a non-Alqueva reservoir must not be flagged, got: %+v", got)
		}
	})
}

// TestSnapshotEmptyIsAuthoritativeNo: a loaded snapshot answering zero
// features is a real "no" — the probe must NOT fall back to the live service.
func TestSnapshotEmptyIsAuthoritativeNo(t *testing.T) {
	liveCalled := false
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		liveCalled = true
		http.Error(w, "live must not be called", http.StatusTeapot)
	}))
	defer live.Close()
	srv := snapshotStub(t, map[string][]snapshotRow{"paap": {}})
	defer srv.Close()

	p := snapshotOrLive("paap", source.ArcGISProbeConfig{LayerURL: live.URL + "/layer/1"},
		source.Options{SnapshotAPI: srv.URL}, nil)
	hits, src, err := p(context.Background(), probeSubject())
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 || liveCalled {
		t.Fatalf("empty snapshot must answer no without live fallback (hits=%d, liveCalled=%v)", len(hits), liveCalled)
	}
	if src.Provenance != "official-snapshot" {
		t.Errorf("provenance = %q", src.Provenance)
	}
}

// TestSnapshotMissingDatasetFallsBack: an unloaded dataset (404) must fall
// back to the live ArcGIS probe.
func TestSnapshotMissingDatasetFallsBack(t *testing.T) {
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"features":[{"attributes":{"paap":"Foz Tua"}}]}`))
	}))
	defer live.Close()
	srv := snapshotStub(t, map[string][]snapshotRow{}) // no datasets loaded
	defer srv.Close()

	p := snapshotOrLive("paap", source.ArcGISProbeConfig{LayerURL: live.URL + "/layer/1"},
		source.Options{SnapshotAPI: srv.URL}, nil)
	hits, src, err := p(context.Background(), probeSubject())
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Prop("paap") != "Foz Tua" {
		t.Fatalf("expected the live fallback answer, got %+v", hits)
	}
	if src.Provenance != "official-live" {
		t.Errorf("provenance = %q, want official-live", src.Provenance)
	}
}

// TestSnapshotDisabledUsesLiveDirectly: with no SnapshotAPI configured the
// composed probe is exactly the live one.
func TestSnapshotDisabledUsesLiveDirectly(t *testing.T) {
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"features":[]}`))
	}))
	defer live.Close()
	p := snapshotOrLive("paap", source.ArcGISProbeConfig{LayerURL: live.URL + "/layer/1"},
		source.Options{}, nil)
	if _, src, err := p(context.Background(), probeSubject()); err != nil || src.Provenance != "official-live" {
		t.Fatalf("expected direct live probe, got src=%+v err=%v", src, err)
	}
}
