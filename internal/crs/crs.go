// Package crs provides the coordinate transformation needed to compute real
// areas from WGS84 geographic coordinates. Portuguese planning data is analysed
// in ETRS89 / PT-TM06 (EPSG:3763), a Transverse Mercator projection in metres.
//
// Inputs to this tool are WGS84 lon/lat (degrees). For mainland Portugal the
// datum difference between WGS84 and ETRS89 is sub-metre, which is negligible
// for the area/percentage figures this tool reports, so we apply the PT-TM06
// projection formula directly to WGS84 coordinates using the GRS80 ellipsoid.
package crs

import (
	"math"

	"github.com/peterstace/simplefeatures/geom"
)

// GRS80 ellipsoid (used by ETRS89).
const (
	a = 6378137.0           // semi-major axis (m)
	f = 1.0 / 298.257222101 // flattening
)

// EPSG:3763 — ETRS89 / PT-TM06 projection parameters.
const (
	lat0 = 39.6682583333333  // latitude of natural origin (deg)
	lon0 = -8.13310833333333 // central meridian (deg)
	k0   = 1.0               // scale factor
	fe   = 0.0               // false easting
	fn   = 0.0               // false northing
)

var (
	e2  = f * (2 - f)   // first eccentricity squared
	ep2 = e2 / (1 - e2) // second eccentricity squared
	m0  = meridionalArc(lat0 * math.Pi / 180)
)

// meridionalArc returns the meridional arc length M for a geodetic latitude
// phi (radians), using the standard series expansion (Snyder, eq. 3-21).
func meridionalArc(phi float64) float64 {
	return a * ((1-e2/4-3*e2*e2/64-5*e2*e2*e2/256)*phi -
		(3*e2/8+3*e2*e2/32+45*e2*e2*e2/1024)*math.Sin(2*phi) +
		(15*e2*e2/256+45*e2*e2*e2/1024)*math.Sin(4*phi) -
		(35*e2*e2*e2/3072)*math.Sin(6*phi))
}

// ToTM06 projects a WGS84 lon/lat (degrees) to ETRS89/PT-TM06 easting/northing
// (metres) using the standard Transverse Mercator forward series (Snyder,
// eqs. 8-9 to 8-11). Accurate to well under a metre for mainland Portugal.
func ToTM06(lon, lat float64) (easting, northing float64) {
	phi := lat * math.Pi / 180
	lam := lon * math.Pi / 180
	lam0rad := lon0 * math.Pi / 180

	sinPhi := math.Sin(phi)
	cosPhi := math.Cos(phi)
	tanPhi := math.Tan(phi)

	N := a / math.Sqrt(1-e2*sinPhi*sinPhi)
	T := tanPhi * tanPhi
	C := ep2 * cosPhi * cosPhi
	A := (lam - lam0rad) * cosPhi
	M := meridionalArc(phi)

	A2 := A * A
	A3 := A2 * A
	A4 := A3 * A
	A5 := A4 * A
	A6 := A5 * A

	easting = fe + k0*N*(A+
		(1-T+C)*A3/6+
		(5-18*T+T*T+72*C-58*ep2)*A5/120)

	northing = fn + k0*(M-m0+
		N*tanPhi*(A2/2+
			(5-T+9*C+4*C*C)*A4/24+
			(61-58*T+T*T+600*C-330*ep2)*A6/720))
	return
}

// ProjectXY is the XY transform used with geom.Geometry.TransformXY. Input XY
// has X=lon, Y=lat (degrees); output has X=easting, Y=northing (metres).
func ProjectXY(xy geom.XY) geom.XY {
	e, n := ToTM06(xy.X, xy.Y)
	return geom.XY{X: e, Y: n}
}

// AreaM2 returns the planar area of a geometry (given in WGS84 lon/lat) in
// square metres, by first projecting it to PT-TM06. Non-areal geometries
// return 0.
func AreaM2(g geom.Geometry) float64 {
	return g.TransformXY(ProjectXY).Area()
}
