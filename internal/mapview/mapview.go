// Package mapview projects planning geometries (WGS84 lon/lat) into a
// self-contained inline SVG map: the municipality boundary, the zoning polygon,
// nearby constraint zones, and the query point/parcel. It emits an HTML
// <figure> that styles itself through CSS classes the surrounding page defines.
package mapview

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/peterstace/simplefeatures/geom"
)

// Role controls how a layer is drawn.
type Role string

const (
	RoleBoundary Role = "boundary"
	RoleZoning   Role = "zoning"
	RolePresent  Role = "present" // constraint that hits the query location
	RoleAbsent   Role = "absent"  // constraint nearby but not at the location
	RoleSubject  Role = "subject"
)

// Layer is one drawable set of geometry.
type Layer struct {
	ID    string // "boundary","zoning","ran","ren","poacb", …
	Label string
	Role  Role
	G     geom.Geometry
}

// Data is everything the map needs.
type Data struct {
	Subject                        geom.Geometry
	SubjectKind                    string // "point" | "polygon"
	MinLon, MinLat, MaxLon, MaxLat float64
	Layers                         []Layer
}

const (
	svgW = 660.0
	svgH = 460.0
	pad  = 16.0
)

// SVG renders the full <figure> (map + legend + scale bar).
func (d *Data) SVG() string {
	midLat := (d.MinLat + d.MaxLat) / 2
	kx := math.Cos(midLat * math.Pi / 180)
	spanX := (d.MaxLon - d.MinLon) * kx
	spanY := (d.MaxLat - d.MinLat)
	if spanX <= 0 || spanY <= 0 {
		return ""
	}
	scale := math.Min((svgW-2*pad)/spanX, (svgH-2*pad)/spanY)
	// centre the drawing
	offX := (svgW - spanX*scale) / 2
	offY := (svgH - spanY*scale) / 2
	proj := func(lon, lat float64) (float64, float64) {
		x := offX + (lon-d.MinLon)*kx*scale
		y := svgH - offY - (lat-d.MinLat)*scale // flip Y
		return x, y
	}

	// The geographic bbox as an SVG rectangle (top-left = MaxLat/MinLon).
	boxX := offX
	boxY := svgH - offY - spanY*scale
	boxW := spanX * scale
	boxH := spanY * scale

	var b strings.Builder
	b.WriteString(`<figure class="pdm-map">`)
	b.WriteString(mapToggle())
	fmt.Fprintf(&b, `<svg viewBox="0 0 %.0f %.0f" role="img" aria-label="Planning map for the query location">`, svgW, svgH)

	// Satellite basemap (behind everything): a single georeferenced ESRI World
	// Imagery export for the same bbox, stretched into the projected box. Hidden
	// by default via CSS — shown only when the reader flips the Satellite toggle.
	fmt.Fprintf(&b,
		`<image class="sat-img" x="%.1f" y="%.1f" width="%.1f" height="%.1f" preserveAspectRatio="none" href="%s"/>`,
		boxX, boxY, boxW, boxH, esriImageURL(d.MinLon, d.MinLat, d.MaxLon, d.MaxLat, boxW, boxH))

	// draw order: boundary, zoning, absent constraints, present constraints, subject
	for _, want := range []Role{RoleBoundary, RoleZoning, RoleAbsent, RolePresent} {
		for _, ly := range d.Layers {
			if ly.Role != want {
				continue
			}
			polys, _ := geomShapes(ly.G, proj)
			for _, p := range polys {
				fmt.Fprintf(&b, `<path class="ly ly-%s" data-role="%s" d="%s"/>`, ly.ID, ly.Role, p)
			}
		}
	}

	// subject
	polys, pts := geomShapes(d.Subject, proj)
	for _, p := range polys {
		fmt.Fprintf(&b, `<path class="subject-poly" d="%s"/>`, p)
	}
	for _, pt := range pts {
		fmt.Fprintf(&b, `<circle class="subject-ring" cx="%.1f" cy="%.1f" r="15"/>`, pt[0], pt[1])
		fmt.Fprintf(&b, `<circle class="subject-dot" cx="%.1f" cy="%.1f" r="5.5"/>`, pt[0], pt[1])
	}

	// scale bar (nice round distance that fits ~1/4 of the width)
	b.WriteString(scaleBar(kx, scale))
	// north arrow
	fmt.Fprintf(&b, `<g transform="translate(%.0f,%.0f)"><text class="map-n" text-anchor="middle">N</text><path d="M0 5 L-4 16 L0 12 L4 16 Z" class="map-narrow"/></g>`, svgW-20, 26.0)
	b.WriteString(`</svg>`)

	b.WriteString(legend(d.Layers, d.SubjectKind))
	b.WriteString(`</figure>`)
	return b.String()
}

// mapToggle is the pure-CSS Map/Satellite switch overlaid on the map. A single
// checkbox drives it; the page's CSS reveals the satellite image and dims the
// vector fills while it is checked.
func mapToggle() string {
	return `<div class="map-toggle"><input type="checkbox" id="pdm-sat" class="sat-toggle" aria-label="Show satellite imagery">` +
		`<label for="pdm-sat"><span class="seg seg-map">Map</span><span class="seg seg-sat">Satellite</span></label></div>`
}

// esriImageURL builds a single-image ESRI World Imagery export for the bbox,
// requested in Web Mercator so it aligns (locally) with the equirectangular
// projection used above. The pixel size keeps the Mercator aspect so the service
// does not expand the bbox; preserveAspectRatio="none" stretches it into the box.
func esriImageURL(minLon, minLat, maxLon, maxLat, boxW, boxH float64) string {
	mercX := func(lon float64) float64 { return lon * 20037508.342789244 / 180 }
	mercY := func(lat float64) float64 {
		return math.Log(math.Tan((90+lat)*math.Pi/360)) / (math.Pi / 180) * 20037508.342789244 / 180
	}
	xmin, ymin, xmax, ymax := mercX(minLon), mercY(minLat), mercX(maxLon), mercY(maxLat)
	w := math.Round(boxW)
	h := w * (ymax - ymin) / (xmax - xmin) // match Mercator aspect
	if h < 1 || math.IsNaN(h) {
		h = math.Round(boxH)
	}
	return fmt.Sprintf(
		"https://services.arcgisonline.com/ArcGIS/rest/services/World_Imagery/MapServer/export?bbox=%.3f,%.3f,%.3f,%.3f&bboxSR=3857&imageSR=3857&size=%.0f,%.0f&format=jpg&transparent=false&f=image",
		xmin, ymin, xmax, ymax, w, math.Round(h))
}

func scaleBar(kx, scale float64) string {
	// metres per svg unit ≈ 111320 / scale (projected units are degrees).
	mpu := 111320.0 / scale
	target := mpu * (svgW / 4) // ~quarter width
	// snap to a nice 1/2/5 × 10^n metres
	pow := math.Pow(10, math.Floor(math.Log10(target)))
	var nice float64
	for _, m := range []float64{5, 2, 1} {
		if m*pow <= target {
			nice = m * pow
			break
		}
	}
	if nice == 0 {
		nice = pow
	}
	length := nice / mpu
	label := fmt.Sprintf("%.0f m", nice)
	if nice >= 1000 {
		label = fmt.Sprintf("%.0f km", nice/1000)
	}
	x0, y0 := pad+6.0, svgH-pad-6.0
	return fmt.Sprintf(
		`<g class="map-scale" transform="translate(%.1f,%.1f)"><line x1="0" y1="0" x2="%.1f" y2="0"/><line x1="0" y1="-5" x2="0" y2="0"/><line x1="%.1f" y1="-5" x2="%.1f" y2="0"/><text x="%.1f" y="-8" text-anchor="middle">%s</text></g>`,
		x0, y0, length, length, length, length/2, label)
}

func legend(layers []Layer, subjectKind string) string {
	seen := map[string]bool{}
	var b strings.Builder
	b.WriteString(`<figcaption class="pdm-legend">`)
	sub := "Query point"
	if subjectKind == "polygon" {
		sub = "Parcel"
	}
	fmt.Fprintf(&b, `<span><i class="key key-subject"></i>%s</span>`, sub)
	for _, ly := range layers {
		if seen[ly.ID] {
			continue
		}
		seen[ly.ID] = true
		suffix := ""
		if ly.Role == RolePresent {
			suffix = " ✓"
		}
		fmt.Fprintf(&b, `<span><i class="key ly-%s" data-role="%s"></i>%s%s</span>`, ly.ID, ly.Role, esc(ly.Label), suffix)
	}
	b.WriteString(`</figcaption>`)
	return b.String()
}

// geomShapes walks a geometry's GeoJSON and returns polygon path strings and
// standalone points (both already projected to SVG space).
func geomShapes(g geom.Geometry, proj func(lon, lat float64) (float64, float64)) (polys []string, pts [][2]float64) {
	if g.IsEmpty() {
		return nil, nil
	}
	raw, err := g.MarshalJSON()
	if err != nil {
		return nil, nil
	}
	var doc struct {
		Type        string            `json:"type"`
		Coordinates json.RawMessage   `json:"coordinates"`
		Geometries  []json.RawMessage `json:"geometries"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return nil, nil
	}
	ring := func(r [][2]float64) string {
		var sb strings.Builder
		last := [2]float64{math.NaN(), math.NaN()}
		started := false
		for _, c := range r {
			x, y := proj(c[0], c[1])
			rx, ry := math.Round(x*10)/10, math.Round(y*10)/10
			if rx == last[0] && ry == last[1] {
				continue
			}
			if !started {
				fmt.Fprintf(&sb, "M%.1f %.1f", rx, ry)
				started = true
			} else {
				fmt.Fprintf(&sb, "L%.1f %.1f", rx, ry)
			}
			last = [2]float64{rx, ry}
		}
		if started {
			sb.WriteString("Z")
		}
		return sb.String()
	}
	switch doc.Type {
	case "Point":
		var c [2]float64
		if json.Unmarshal(doc.Coordinates, &c) == nil {
			x, y := proj(c[0], c[1])
			pts = append(pts, [2]float64{math.Round(x*10) / 10, math.Round(y*10) / 10})
		}
	case "Polygon":
		var rings [][][2]float64
		if json.Unmarshal(doc.Coordinates, &rings) == nil {
			for _, r := range rings {
				if p := ring(r); p != "" {
					polys = append(polys, p)
				}
			}
		}
	case "MultiPolygon":
		var mp [][][][2]float64
		if json.Unmarshal(doc.Coordinates, &mp) == nil {
			for _, poly := range mp {
				for _, r := range poly {
					if p := ring(r); p != "" {
						polys = append(polys, p)
					}
				}
			}
		}
	case "GeometryCollection":
		for _, gr := range doc.Geometries {
			if sub, err := geom.UnmarshalGeoJSON(gr, geom.NoValidate{}); err == nil {
				sp, spts := geomShapes(sub, proj)
				polys = append(polys, sp...)
				pts = append(pts, spts...)
			}
		}
	}
	return polys, pts
}

func esc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
