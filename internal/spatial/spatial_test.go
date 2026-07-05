package spatial

import (
	"testing"

	"github.com/peterstace/simplefeatures/geom"
)

const validFC = `{"type":"FeatureCollection","features":[
 {"type":"Feature","properties":{"k":"a"},"geometry":{"type":"Polygon","coordinates":[[[0,0],[2,0],[2,2],[0,2],[0,0]]]}},
 {"type":"Feature","properties":{"k":"b"},"geometry":{"type":"Polygon","coordinates":[[[1,1],[3,1],[3,3],[1,3],[1,1]]]}}
]}`

// A bowtie/self-touching ring that fails OGC validation but must not break the load.
const mixedFC = `{"type":"FeatureCollection","features":[
 {"type":"Feature","properties":{"k":"good"},"geometry":{"type":"Polygon","coordinates":[[[0,0],[2,0],[2,2],[0,2],[0,0]]]}},
 {"type":"Feature","properties":{"k":"bowtie"},"geometry":{"type":"Polygon","coordinates":[[[0,0],[2,2],[2,0],[0,2],[0,0]]]}}
]}`

func TestLoadFeatureCollection(t *testing.T) {
	feats, err := LoadFeatureCollection([]byte(validFC))
	if err != nil {
		t.Fatal(err)
	}
	if len(feats) != 2 {
		t.Fatalf("want 2 features, got %d", len(feats))
	}
	if feats[0].Prop("k") != "a" {
		t.Fatalf("prop mismatch: %q", feats[0].Prop("k"))
	}
}

func TestLoadRejectsNonFeatureCollection(t *testing.T) {
	// An HTTP-200 error payload (no "features" array) must error, not silently
	// become an empty layer.
	for _, bad := range []string{`{"error":{"code":500,"message":"boom"}}`, `{"type":"Error"}`} {
		if _, err := LoadFeatureCollection([]byte(bad)); err == nil {
			t.Errorf("expected error for non-FeatureCollection payload %q", bad)
		}
	}
	// An empty-but-valid collection must succeed.
	if feats, err := LoadFeatureCollection([]byte(`{"type":"FeatureCollection","features":[]}`)); err != nil || len(feats) != 0 {
		t.Errorf("empty FC should load with 0 features, got %d err=%v", len(feats), err)
	}
}

func TestLoadToleratesInvalidGeometry(t *testing.T) {
	feats, err := LoadFeatureCollection([]byte(mixedFC))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(feats) != 2 {
		t.Fatalf("both features (including the bowtie) should load, got %d", len(feats))
	}
}

func TestPropCollapsesWhitespace(t *testing.T) {
	f := Feature{Props: map[string]any{"x": "Solo Urbano –  Espaços"}}
	if got := f.Prop("x"); got != "Solo Urbano – Espaços" {
		t.Fatalf("whitespace not collapsed: %q", got)
	}
}

func TestIntersectsPointInPolygon(t *testing.T) {
	sq, _ := geom.UnmarshalGeoJSON([]byte(`{"type":"Polygon","coordinates":[[[0,0],[4,0],[4,4],[0,4],[0,0]]]}`))
	if !Intersects(Point(2, 2), sq) {
		t.Fatal("point (2,2) should be inside the square")
	}
	if Intersects(Point(9, 9), sq) {
		t.Fatal("point (9,9) should be outside the square")
	}
}

func TestCoverageDedupesOverlap(t *testing.T) {
	// subject 0..4 square; two features overlapping each other. Coverage must be
	// the union area (16 minus the uncovered corner), NOT the double-counted sum.
	subject, _ := geom.UnmarshalGeoJSON([]byte(`{"type":"Polygon","coordinates":[[[0,0],[4,0],[4,4],[0,4],[0,0]]]}`))
	feats, _ := LoadFeatureCollection([]byte(validFC)) // squares 0..2 and 1..3, overlapping in 1..2
	cov, err := Coverage(subject, feats)
	if err != nil {
		t.Fatal(err)
	}
	area := cov.Area() // planar area in coordinate units (degrees² here, fine for the check)
	// union of [0,2]²(=4) and [1,3]²(=4) overlapping in [1,2]²(=1) → 7.
	if area < 6.99 || area > 7.01 {
		t.Fatalf("coverage area should be 7 (union), got %v", area)
	}
}

func TestParseInputGeometryShapes(t *testing.T) {
	cases := map[string]string{
		"bare":    `{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,1],[0,0]]]}`,
		"feature": `{"type":"Feature","properties":{},"geometry":{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,1],[0,0]]]}}`,
		"fc":      validFC,
	}
	for name, gj := range cases {
		g, err := ParseInputGeometry([]byte(gj))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if g.IsEmpty() {
			t.Fatalf("%s: empty geometry", name)
		}
	}
}
