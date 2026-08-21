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
	"github.com/bernardosimoes/pdm/internal/ai"
	"github.com/bernardosimoes/pdm/internal/boot"
	"github.com/bernardosimoes/pdm/internal/mapview"
	"github.com/bernardosimoes/pdm/internal/munidoc"
	"github.com/bernardosimoes/pdm/internal/planos"
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
  pdm analyse <file.geojson|lat lon> [--tier ...]    AI-written analysis report
  pdm supported                   show municipality coverage/support levels
  pdm municipalities              list every municipality as JSON (name, code,
                                  district, centroid, bbox, regulamento, plans)
  pdm serve [--listen addr]       run as a persistent localhost HTTP daemon
                                  (engine built once; see /v1/report)
  pdm version                     print version
  pdm help                        show this help

OPTIONS:
  --format <text|json|markdown|html|ndjson>   output format (default: text;
                                  analyse: html); ndjson streams events as
                                  sources resolve
  --tier <basic|premium>          analysis model tier (default: basic)
  --live                          fetch from official geoservices (falls back to bundled)
  --no-cache                      do not read/write the local cache
  --cache-dir <dir>               override the cache directory
  --truth-api <url>               pdms recorded-zoning mirror, consulted before the
                                  official sources for point and fully covered parcel
                                  queries on generic municipalities (--live bypasses it; set via
                                  the PDM_TRUTH_API env var; --truth-api= disables)
  --listen <addr>                 serve: listen address (default 127.0.0.1:8787;
                                  also set via the PDM_LISTEN env var)

EXAMPLES:
  pdm 39.60 -8.41
  pdm point 39.60 -8.41
  pdm polygon ./parcel.geojson
  pdm report ./parcel.geojson --format json
  pdm report 39.60 -8.41 --format markdown
  pdm analyse 39.60 -8.41 --tier premium > analysis.html

NOTE: "pdm analyse" calls the Anthropic API and needs ANTHROPIC_API_KEY set.

NOTE: coordinates are latitude then longitude, in WGS84 decimal degrees.
      Portuguese longitudes are negative (west of Greenwich).
`

type options struct {
	format    report.Format
	formatSet bool
	tier      string
	live      bool
	noCache   bool
	cacheDir  string
	// truthAPI overrides the PDM_TRUTH_API env var when truthAPISet — an
	// explicit empty value (--truth-api=) disables the mirror even with the
	// env var set.
	truthAPI    string
	truthAPISet bool
	listen      string
}

// Run parses args (excluding the program name) and executes, returning an exit
// code.
func Run(args []string, stdout, stderr io.Writer) int {
	opts := options{format: report.FormatText, tier: string(ai.TierBasic)}
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
		return runSupported(stdout, stderr)
	case "municipalities":
		return runMunicipalities(stdout, stderr)
	case "serve":
		return runServe(opts, stdout, stderr)
	case "point":
		return runPoint(rest, opts, stdout, stderr)
	case "polygon":
		return runPolygon(rest, opts, stdout, stderr)
	case "report":
		return runReport(rest, opts, stdout, stderr)
	case "analyse", "analyze":
		return runAnalyse(rest, opts, stdout, stderr)
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

func runSupported(stdout, stderr io.Writer) int {
	count := 0
	if resolver, err := admin.NewResolver(data.Municipalities); err == nil {
		count = resolver.Count()
	}
	fmt.Fprintf(stdout, "Municipalities with a dedicated adapter (richer local sources):\n")
	for _, m := range registry.Supported() {
		fmt.Fprintf(stdout, "  - %s\n", m)
	}
	fmt.Fprintf(stdout, "  (Tomar additionally bundles its municipal layers and parsed regulation;\n")
	fmt.Fprintf(stdout, "  Mafra and Ourém query their municipal geoservices live.)\n")
	fmt.Fprintf(stdout, "\nEvery other mainland municipality (%d total in CAOP) is supported for:\n", count)
	fmt.Fprintf(stdout, "  - zoning: national DGT CRUS dataset, queried live (cached locally);\n")
	fmt.Fprintf(stdout, "  - constraints: RAN and REN (DGT/SNIT SRUP), áreas protegidas, albufeiras\n")
	fmt.Fprintf(stdout, "    classificadas and coastal plans/programs (POC/POOC/POAAP/PAAP via\n")
	fmt.Fprintf(stdout, "    APA/SNIAmb), probed live server-side; Rede Natura 2000 (ZEC/ZPE) and\n")
	fmt.Fprintf(stdout, "    perigosidade de incêndio rural (classes alta/muito alta) evaluated as\n")
	fmt.Fprintf(stdout, "    geometry with real overlap areas;\n")
	fmt.Fprintf(stdout, "  - special instruments: a bundled registry of %d planos/programas especiais\n", planos.Count())
	fmt.Fprintf(stdout, "    (albufeiras, orla costeira, estuários, áreas protegidas) per municipality.\n")
	fmt.Fprintf(stdout, "  The parsed written regulation (Regulamento) remains per-municipality work.\n")
	fmt.Fprintf(stdout, "\nKnown national data gaps (reported as \"unknown\", never as \"no\"):\n")
	fmt.Fprintf(stdout, "  ~50 municipalities lack their REN delimitation in SNIT and 6 lack a\n")
	fmt.Fprintf(stdout, "  published RAN; Lisboa, Porto and Amadora genuinely have no RAN.\n")
	fmt.Fprintf(stdout, "\nAzores and Madeira are not yet covered (regional services pending).\n")
	return 0
}

// runMunicipalities emits every municipality in the CAOP boundary dataset as a
// single JSON document (see internal/munidoc — the shape is a wire contract
// consumed by server.js).
func runMunicipalities(stdout, stderr io.Writer) int {
	resolver, err := admin.NewResolver(data.Municipalities)
	if err != nil {
		fmt.Fprintf(stderr, "error: load administrative boundaries: %v\n", err)
		return 1
	}
	if err := munidoc.Encode(stdout, munidoc.Build(resolver)); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
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
	// Generous timeout: municipalities without bundled data fetch zoning and
	// national constraints live from the DGT/APA geoservices, which are slow when
	// cold and periodically overloaded. The budget must cover a few time-boxed
	// retries per layer (see perAttemptTimeout) so a transient hang isn't fatal.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	if opts.format == report.FormatNDJSON {
		return streamReport(stdout, stderr,
			func(emit query.Emit) (any, error) { return eng.PointStream(ctx, lon, lat, emit) },
			nil) // PointStream reuses its zoning geometry to emit the locator map.
	}
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
	g, err := loadInput(path)
	if err != nil {
		fmt.Fprintf(stderr, "error: reading %s: %v\n", path, err)
		return 1
	}
	eng, err := buildEngine(opts)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	if opts.format == report.FormatNDJSON {
		return streamReport(stdout, stderr,
			func(emit query.Emit) (any, error) { return eng.PolygonStream(ctx, g, emit) },
			func() *mapview.Data {
				mapCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer cancel()
				return eng.PolygonMap(mapCtx, g)
			})
	}
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

// loadInput reads an input geometry from a GeoJSON file or stdin ("-").
func loadInput(path string) (geom.Geometry, error) {
	if path == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return geom.Geometry{}, fmt.Errorf("reading stdin: %w", err)
		}
		return spatial.ParseInputGeometry(data)
	}
	return spatial.LoadInputGeometry(path)
}

// streamReport runs a query in NDJSON streaming mode (see query.StreamNDJSON,
// shared with `pdm serve`): the engine emits meta and per-layer events as they
// complete, the locator map is generated concurrently, and the complete result
// is the terminal event — so one process yields what previously took separate
// json and html runs.
func streamReport(stdout, stderr io.Writer, run func(query.Emit) (any, error), buildMap func() *mapview.Data) int {
	if err := query.StreamNDJSON(stdout, run, buildMap); err != nil {
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

// newGenerator builds the AI client for a tier. It is a package variable so
// tests can substitute a stub generator.
var newGenerator = func(tier ai.Tier) (ai.Generator, error) {
	return ai.New(tier)
}

// runAnalyse queries a point or parcel and produces an AI-written analysis
// report grounded in the query result.
func runAnalyse(args []string, opts options, stdout, stderr io.Writer) int {
	tier, err := ai.ParseTier(opts.tier)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	if !opts.formatSet {
		opts.format = report.FormatHTML
	}
	if opts.format == report.FormatText || opts.format == report.FormatNDJSON {
		fmt.Fprintf(stderr, "error: analyse supports --format html, markdown, or json\n")
		return 2
	}

	// Fail fast (missing/empty ANTHROPIC_API_KEY) before running the query.
	gen, err := newGenerator(tier)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	eng, err := buildEngine(opts)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	// Wider timeout than plain queries: it spans a possible live zoning fetch
	// plus the model generation.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	var (
		view    report.AnalysisView
		payload ai.Payload
		result  any
		mapSVG  string
	)
	switch {
	case len(args) >= 2 && isFloat(args[0]) && isFloat(args[1]):
		lat, _ := strconv.ParseFloat(args[0], 64)
		lon, _ := strconv.ParseFloat(args[1], 64)
		if err := validateLatLon(lat, lon); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 2
		}
		res, err := eng.Point(ctx, lon, lat)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		if payload, err = ai.BuildPointPayload(res); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		view, result = report.NewPointAnalysisView(res), res
		if opts.format == report.FormatHTML {
			if m := eng.PointMap(ctx, lon, lat); m != nil {
				mapSVG = m.SVG()
			}
		}
	case len(args) >= 1 && (args[0] == "-" || fileExists(args[0])):
		g, err := loadInput(args[0])
		if err != nil {
			fmt.Fprintf(stderr, "error: reading %s: %v\n", args[0], err)
			return 1
		}
		res, err := eng.Polygon(ctx, g)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		if payload, err = ai.BuildPolygonPayload(res); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		view, result = report.NewPolygonAnalysisView(res), res
		if opts.format == report.FormatHTML {
			if m := eng.PolygonMap(ctx, g); m != nil {
				mapSVG = m.SVG()
			}
		}
	default:
		fmt.Fprintf(stderr, "error: analyse needs a GeoJSON file or <lat> <lon>\n")
		return 2
	}

	fmt.Fprintf(stderr, "generating %s analysis (%s)…\n", tier, ai.ModelFor(tier))
	analysis, err := gen.Generate(ctx, payload)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	tierLabel := fmt.Sprintf("análise AI · %s · %s", tier, ai.ModelFor(tier))
	switch opts.format {
	case report.FormatJSON:
		err = report.JSON(stdout, struct {
			Result   any          `json:"result"`
			Analysis *ai.Analysis `json:"analysis"`
		}{result, analysis})
	case report.FormatMarkdown:
		err = report.AnalysisMarkdown(stdout, view, analysis, payload.DataGaps, tierLabel)
	default:
		err = report.AnalysisHTML(stdout, view, analysis, payload.DataGaps, mapSVG, tierLabel)
	}
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func isFloat(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

func buildEngine(opts options) (*query.Engine, error) {
	resolver, freguesias, err := boot.LoadResolvers()
	if err != nil {
		return nil, err
	}
	c, err := boot.NewCache(opts.cacheDir, opts.noCache)
	if err != nil {
		return nil, err
	}
	so := source.Options{Live: opts.live, Cache: c}
	if err := boot.FetchOptionsFromEnv(&so); err != nil {
		return nil, err
	}
	truthAPI := os.Getenv("PDM_TRUTH_API")
	if opts.truthAPISet {
		truthAPI = opts.truthAPI
	}
	if so.TruthAPI, err = boot.ValidateTruthAPI(truthAPI); err != nil {
		return nil, err
	}
	// The snapshot store lives on the same server as the truth mirror; one
	// URL configures both (and --truth-api= disables both).
	so.SnapshotAPI = so.TruthAPI
	eng := query.New(resolver, so)
	// Freguesia labelling is optional — a broken dataset must not block queries.
	if freguesias != nil {
		eng.SetFreguesias(freguesias)
	}
	return eng, nil
}

func validateLatLon(lat, lon float64) error {
	return spatial.ValidateLatLon(lat, lon)
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
				opts.formatSet = true
			case "tier":
				v, err := needValue(name, val, hasInline, args, &i)
				if err != nil {
					return nil, err
				}
				opts.tier = v
			case "cache-dir":
				v, err := needValue(name, val, hasInline, args, &i)
				if err != nil {
					return nil, err
				}
				opts.cacheDir = v
			case "truth-api":
				// Inline-empty (--truth-api=) is a valid value: an explicit disable
				// that overrides PDM_TRUTH_API.
				v, err := needValue(name, val, hasInline, args, &i)
				if err != nil {
					return nil, err
				}
				opts.truthAPI = v
				opts.truthAPISet = true
			case "listen":
				v, err := needValue(name, val, hasInline, args, &i)
				if err != nil {
					return nil, err
				}
				opts.listen = v
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
