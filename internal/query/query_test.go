package query_test

import (
	"context"
	"testing"

	"github.com/bernardosimoes/pdm/data"
	"github.com/bernardosimoes/pdm/internal/admin"
	"github.com/bernardosimoes/pdm/internal/model"
	"github.com/bernardosimoes/pdm/internal/query"
	"github.com/bernardosimoes/pdm/internal/source"
	"github.com/bernardosimoes/pdm/internal/spatial"
)

func newEngine(t *testing.T) *query.Engine {
	t.Helper()
	r, err := admin.NewResolver(data.Municipalities)
	if err != nil {
		t.Fatal(err)
	}
	return query.New(r, source.Options{Live: false})
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

func TestPointUnsupportedMunicipality(t *testing.T) {
	res, err := newEngine(t).Point(context.Background(), -8.60, 39.68) // Ourém
	if err != nil {
		t.Fatal(err)
	}
	if res.Municipality != "Ourém" {
		t.Fatalf("expected Ourém, got %q", res.Municipality)
	}
	if res.Supported {
		t.Error("Ourém should not be supported")
	}
	if res.Confidence != model.ConfidenceLow {
		t.Errorf("unsupported result should be low confidence, got %s", res.Confidence)
	}
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
