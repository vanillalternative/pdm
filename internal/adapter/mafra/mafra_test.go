package mafra

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/bernardosimoes/pdm/internal/source"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGeoMafraZoningCombinesUrbanAndRuralLayers(t *testing.T) {
	const urban = `{"type":"FeatureCollection","features":[{"type":"Feature","properties":{"OBJECTID":1535,"CLASSIFICACAO":"Solo urbano","QUALIFICACAO":"Espaços habitacionais - Áreas consolidadas"},"geometry":{"type":"Polygon","coordinates":[[[-9.42,38.96],[-9.41,38.96],[-9.41,38.98],[-9.42,38.98],[-9.42,38.96]]]}}]}`
	const empty = `{"type":"FeatureCollection","features":[]}`
	var mu sync.Mutex
	var paths []string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		body := empty
		if strings.Contains(r.URL.Path, "/66/query") {
			body = urban
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": []string{"application/geo+json"}}}, nil
	})}
	bb := source.BBox{MinLon: -9.42056, MinLat: 38.96325, MaxLon: -9.41056, MaxLat: 38.97325}
	a := New()
	layer := a.Layers(source.Options{Live: true, HTTP: client, BBox: &bb})[0]
	loaded, err := layer.Loader(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || len(loaded.Features) != 1 {
		t.Fatalf("expected two municipal requests and one feature, paths=%v features=%d", paths, len(loaded.Features))
	}
	c := layer.Classify(loaded.Features[0])
	if c.Class != "Solo urbano" || c.Subclass != "Espaços habitacionais - Áreas consolidadas" {
		t.Fatalf("unexpected classification: %+v", c)
	}
}
