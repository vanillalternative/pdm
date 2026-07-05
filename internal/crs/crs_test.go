package crs

import (
	"math"
	"strconv"
	"testing"

	"github.com/peterstace/simplefeatures/geom"
)

func TestOriginProjectsToZero(t *testing.T) {
	e, n := ToTM06(lon0, lat0)
	if math.Abs(e) > 1e-3 || math.Abs(n) > 1e-3 {
		t.Fatalf("origin should map to (0,0), got (%f, %f)", e, n)
	}
}

func TestEastingIncreasesEastward(t *testing.T) {
	eW, _ := ToTM06(-8.5, 39.6)
	eE, _ := ToTM06(-8.3, 39.6)
	if !(eE > eW) {
		t.Fatalf("easting should increase eastward: west=%f east=%f", eW, eE)
	}
}

func TestAreaOfKnownBox(t *testing.T) {
	// A 0.01° x 0.01° box near Tomar. Expected area from local metres-per-degree.
	const lat = 39.60
	const d = 0.01
	poly := boxPolygon(-8.41, lat, d)

	got := AreaM2(poly)

	mPerDegLon := 111320.0 * math.Cos(lat*math.Pi/180)
	mPerDegLat := 111132.0
	want := (d * mPerDegLon) * (d * mPerDegLat)
	if rel := math.Abs(got-want) / want; rel > 0.02 {
		t.Fatalf("area %v m² differs from expected %v m² by %.1f%% (>2%%)", got, want, rel*100)
	}
}

func boxPolygon(lon, lat, d float64) geom.Geometry {
	c := func(x, y float64) string {
		return "[" + strconv.FormatFloat(x, 'f', -1, 64) + "," + strconv.FormatFloat(y, 'f', -1, 64) + "]"
	}
	gj := []byte(`{"type":"Polygon","coordinates":[[` +
		c(lon, lat) + "," + c(lon+d, lat) + "," + c(lon+d, lat+d) + "," +
		c(lon, lat+d) + "," + c(lon, lat) + `]]}`)
	g, err := geom.UnmarshalGeoJSON(gj)
	if err != nil {
		panic(err)
	}
	return g
}
