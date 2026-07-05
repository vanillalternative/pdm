// Package cli implements the pdm command-line interface. It uses a small
// hand-rolled argument parser rather than a flag library because Portuguese
// longitudes are negative (e.g. -8.41) and must be accepted as positional
// coordinates, not mistaken for flags.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bernardosimoes/pdm/data"
	"github.com/bernardosimoes/pdm/internal/admin"
	"github.com/bernardosimoes/pdm/internal/cache"
	"github.com/bernardosimoes/pdm/internal/query"
	"github.com/bernardosimoes/pdm/internal/registry"
	"github.com/bernardosimoes/pdm/internal/report"
	"github.com/bernardosimoes/pdm/internal/source"
	"github.com/bernardosimoes/pdm/internal/spatial"
	"github.com/peterstace/simplefeatures/geom"
)

// Version is the tool version (overridable at build time).
var Version = "0.1.0"

const usage = `pdm — spatial query of Portuguese municipal PDM/IGT planning data

USAGE:
  pdm <lat> <lon>                 query a coordinate (shorthand)
  pdm point <lat> <lon>           query a coordinate
  pdm polygon <file.geojson>      query a parcel polygon
  pdm report <file.geojson|lat lon> [--format ...]   full report
  pdm supported                   list supported municipalities
  pdm version                     print version
  pdm help                        show this help

OPTIONS:
  --format <text|json|markdown|html>   output format (default: text)
  --live                          fetch from official geoservices (falls back to bundled)
  --no-cache                      do not read/write the local cache
  --cache-dir <dir>               override the cache directory

EXAMPLES:
  pdm 39.60 -8.41
  pdm point 39.60 -8.41
  pdm polygon ./parcel.geojson
  pdm report ./parcel.geojson --format json
  pdm report 39.60 -8.41 --format markdown

NOTE: coordinates are latitude then longitude, in WGS84 decimal degrees.
      Portuguese longitudes are negative (west of Greenwich).
`

type options struct {
	format   report.Format
	live     bool
	noCache  bool
	cacheDir string
}

// Run parses args (excluding the program name) and executes, returning an exit
// code.
func Run(args []string, stdout, stderr io.Writer) int {
	opts := options{format: report.FormatText}
	positionals, err := parse(args, &opts)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n\n%s", err, usage)
		return 2
	}

	if len(positionals) == 0 {
		fmt.Fprint(stdout, usage)
		return 0
	}

	cmd := positionals[0]
	rest := positionals[1:]

	switch strings.ToLower(cmd) {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "version", "--version":
		fmt.Fprintf(stdout, "pdm %s\n", Version)
		return 0
	case "supported":
		fmt.Fprintf(stdout, "Supported municipalities:\n")
		for _, m := range registry.Supported() {
			fmt.Fprintf(stdout, "  - %s\n", m)
		}
		return 0
	case "point":
		return runPoint(rest, opts, stdout, stderr)
	case "polygon":
		return runPolygon(rest, opts, stdout, stderr)
	case "report":
		return runReport(rest, opts, stdout, stderr)
	default:
		// Shorthand: `pdm <lat> <lon>` or `pdm <file.geojson>`.
		return runInferred(positionals, opts, stdout, stderr)
	}
}

func runInferred(positionals []string, opts options, stdout, stderr io.Writer) int {
	if len(positionals) >= 2 {
		if _, err1 := strconv.ParseFloat(positionals[0], 64); err1 == nil {
			return runPoint(positionals, opts, stdout, stderr)
		}
	}
	if len(positionals) == 1 {
		if _, err := strconv.ParseFloat(positionals[0], 64); err == nil {
			fmt.Fprintf(stderr, "error: a coordinate needs both <lat> and <lon>, e.g. `pdm 39.60 -8.41`\n")
			return 2
		}
		return runPolygon(positionals, opts, stdout, stderr)
	}
	fmt.Fprintf(stderr, "error: unrecognized command %q\n\n%s", positionals[0], usage)
	return 2
}

func runPoint(args []string, opts options, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprintf(stderr, "error: point needs <lat> <lon>\n")
		return 2
	}
	lat, err := strconv.ParseFloat(args[0], 64)
	if err != nil {
		fmt.Fprintf(stderr, "error: invalid latitude %q: %v\n", args[0], err)
		return 2
	}
	lon, err := strconv.ParseFloat(args[1], 64)
	if err != nil {
		fmt.Fprintf(stderr, "error: invalid longitude %q: %v\n", args[1], err)
		return 2
	}
	if err := validateLatLon(lat, lon); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	eng, err := buildEngine(opts)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := eng.Point(ctx, lon, lat)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if opts.format == report.FormatHTML {
		svg := ""
		if m := eng.PointMap(ctx, lon, lat); m != nil {
			svg = m.SVG()
		}
		if err := report.PointHTML(stdout, res, svg); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		return 0
	}
	if err := report.Point(stdout, res, opts.format); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func runPolygon(args []string, opts options, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintf(stderr, "error: polygon needs a GeoJSON file\n")
		return 2
	}
	path := args[0]
	var g geom.Geometry
	var err error
	if path == "-" {
		data, rerr := io.ReadAll(os.Stdin)
		if rerr != nil {
			fmt.Fprintf(stderr, "error: reading stdin: %v\n", rerr)
			return 1
		}
		g, err = spatial.ParseInputGeometry(data)
	} else {
		g, err = spatial.LoadInputGeometry(path)
	}
	if err != nil {
		fmt.Fprintf(stderr, "error: reading %s: %v\n", path, err)
		return 1
	}
	eng, err := buildEngine(opts)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	res, err := eng.Polygon(ctx, g)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if opts.format == report.FormatHTML {
		svg := ""
		if m := eng.PolygonMap(ctx, g); m != nil {
			svg = m.SVG()
		}
		if err := report.PolygonHTML(stdout, res, svg); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		return 0
	}
	if err := report.Polygon(stdout, res, opts.format); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

// runReport accepts either a GeoJSON file (polygon) or two coordinates (point).
func runReport(args []string, opts options, stdout, stderr io.Writer) int {
	if len(args) >= 2 {
		if _, err := strconv.ParseFloat(args[0], 64); err == nil {
			return runPoint(args, opts, stdout, stderr)
		}
	}
	if len(args) >= 1 {
		if fileExists(args[0]) {
			return runPolygon(args, opts, stdout, stderr)
		}
	}
	fmt.Fprintf(stderr, "error: report needs a GeoJSON file or <lat> <lon>\n")
	return 2
}

func buildEngine(opts options) (*query.Engine, error) {
	resolver, err := admin.NewResolver(data.Municipalities)
	if err != nil {
		return nil, fmt.Errorf("load administrative boundaries: %w", err)
	}
	c, err := cache.New(cache.Options{
		Dir:      opts.cacheDir,
		TTL:      7 * 24 * time.Hour,
		Disabled: opts.noCache,
	})
	if err != nil {
		return nil, fmt.Errorf("init cache: %w", err)
	}
	so := source.Options{Live: opts.live, Cache: c}
	return query.New(resolver, so), nil
}

func validateLatLon(lat, lon float64) error {
	if lat < -90 || lat > 90 {
		return fmt.Errorf("latitude %.5f out of range [-90, 90]", lat)
	}
	if lon < -180 || lon > 180 {
		return fmt.Errorf("longitude %.5f out of range [-180, 180]", lon)
	}
	// Gentle sanity hint for swapped lat/lon in the Portuguese context.
	if lat < 0 && lon > 0 {
		return fmt.Errorf("coordinates look swapped: expected <lat> <lon> (Portugal: lat ~37..42, lon ~-9..-6)")
	}
	return nil
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// parse extracts flags into opts and returns the positional arguments. Tokens
// beginning with "-" followed by a digit or "." are treated as positional
// (negative numbers), not flags.
func parse(args []string, opts *options) ([]string, error) {
	var positionals []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" { // end-of-options: everything after is positional
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if isFlag(a) {
			name, val, hasInline := splitFlag(a)
			switch name {
			case "format", "f":
				v, err := needValue(name, val, hasInline, args, &i)
				if err != nil {
					return nil, err
				}
				fm, err := report.ParseFormat(v)
				if err != nil {
					return nil, err
				}
				opts.format = fm
			case "cache-dir":
				v, err := needValue(name, val, hasInline, args, &i)
				if err != nil {
					return nil, err
				}
				opts.cacheDir = v
			case "live":
				opts.live = true
			case "no-cache":
				opts.noCache = true
			case "help", "h":
				positionals = append(positionals, "help")
			case "version":
				positionals = append(positionals, "version")
			default:
				return nil, fmt.Errorf("unknown flag --%s", name)
			}
			continue
		}
		positionals = append(positionals, a)
	}
	return positionals, nil
}

// isFlag reports whether a token is a flag (as opposed to a positional, which
// includes negative numbers like -8.41).
func isFlag(a string) bool {
	if !strings.HasPrefix(a, "-") || a == "-" {
		return false
	}
	// "--foo" is always a flag.
	if strings.HasPrefix(a, "--") {
		return true
	}
	// "-8.41" / "-.5" are negative numbers (positional).
	c := a[1]
	if c >= '0' && c <= '9' || c == '.' {
		return false
	}
	return true
}

// splitFlag parses "--name=value" / "--name" / "-n" into (name, value, hasInline).
func splitFlag(a string) (name, val string, hasInline bool) {
	a = strings.TrimLeft(a, "-")
	if idx := strings.IndexByte(a, '='); idx >= 0 {
		return a[:idx], a[idx+1:], true
	}
	return a, "", false
}

func needValue(name, inline string, hasInline bool, args []string, i *int) (string, error) {
	if hasInline {
		return inline, nil
	}
	// Don't swallow a following flag as this flag's value.
	if *i+1 >= len(args) || isFlag(args[*i+1]) {
		return "", fmt.Errorf("flag --%s needs a value", name)
	}
	*i++
	return args[*i], nil
}
