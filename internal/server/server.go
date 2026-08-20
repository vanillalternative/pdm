// Package server implements `pdm serve`: a long-lived localhost HTTP daemon
// exposing the query engine so web/server.js can call it per request instead
// of spawning a fresh pdm process (which re-parses the embedded CAOP datasets
// every time, ~240 ms of fixed cost per query).
//
// The endpoints mirror the CLI contracts exactly: /v1/municipalities is
// byte-identical to `pdm municipalities`, and /v1/report?format=ndjson emits
// the same event stream as `pdm report --format ndjson` (meta first, terminal
// result/error last, via query.StreamNDJSON). Validation failures answer 422
// with {"ok":false,"error":...} before any body, so the Node client can trust
// the status code instead of sniffing the first stdout line.
package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/bernardosimoes/pdm/internal/admin"
	"github.com/bernardosimoes/pdm/internal/boot"
	"github.com/bernardosimoes/pdm/internal/cache"
	"github.com/bernardosimoes/pdm/internal/mapview"
	"github.com/bernardosimoes/pdm/internal/munidoc"
	"github.com/bernardosimoes/pdm/internal/query"
	"github.com/bernardosimoes/pdm/internal/report"
	"github.com/bernardosimoes/pdm/internal/source"
	"github.com/bernardosimoes/pdm/internal/spatial"
	"github.com/peterstace/simplefeatures/geom"
)

// queryBudget matches the CLI's per-query timeout: live layers on cold
// geoservices need room for a few time-boxed retry attempts.
const queryBudget = 150 * time.Second

// maxConcurrentQueries bounds in-flight report queries. Each query fans out
// goroutines per layer and may call back into the web server's truth mirror,
// so an unbounded daemon could amplify one traffic spike into many loopback
// requests. Waiters honor the request context.
const maxConcurrentQueries = 16

// maxBodyBytes caps a POSTed GeoJSON parcel (the CLI reads files of any size,
// but over HTTP an explicit bound is cheap insurance).
const maxBodyBytes = 16 << 20

// Config carries the process-wide settings resolved by the CLI entry point.
type Config struct {
	CacheDir string
	TruthAPI string // validated base URL; "" disables the mirror
	Version  string
}

// Server holds the state that used to be rebuilt on every pdm invocation.
type Server struct {
	resolver   *admin.Resolver
	freguesias *admin.FreguesiaResolver
	cache      *cache.Cache
	baseOpts   source.Options
	muniJSON   []byte
	version    string
	binHash    string
	sem        chan struct{}
}

// New builds the engine state once: resolvers, cache, fetch budget, and the
// pre-encoded municipalities document.
func New(cfg Config) (*Server, error) {
	resolver, freguesias, err := boot.LoadResolvers()
	if err != nil {
		return nil, err
	}
	c, err := boot.NewCache(cfg.CacheDir, false)
	if err != nil {
		return nil, err
	}
	base := source.Options{Cache: c, TruthAPI: cfg.TruthAPI, SnapshotAPI: cfg.TruthAPI}
	if err := boot.FetchOptionsFromEnv(&base); err != nil {
		return nil, err
	}

	var muniBuf bytes.Buffer
	if err := munidoc.Encode(&muniBuf, munidoc.Build(resolver)); err != nil {
		return nil, err
	}

	s := &Server{
		resolver:   resolver,
		freguesias: freguesias,
		cache:      c,
		baseOpts:   base,
		muniJSON:   muniBuf.Bytes(),
		version:    cfg.Version,
		binHash:    executableHash(),
		sem:        make(chan struct{}, maxConcurrentQueries),
	}
	return s, nil
}

// executableHash mirrors server.js's cache-key fingerprint: the first 12 hex
// chars of the binary's sha256. Empty on failure (the endpoint still answers).
func executableHash() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	f, err := os.Open(exe)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:12]
}

// Handler returns the daemon's HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("GET /v1/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"version": s.version, "binhash": s.binHash})
	})
	mux.HandleFunc("GET /v1/municipalities", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(s.muniJSON)
	})
	mux.HandleFunc("GET /v1/report", s.handlePointReport)
	mux.HandleFunc("POST /v1/report", s.handlePolygonReport)
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, format string, args ...any) {
	writeJSON(w, status, map[string]any{"ok": false, "error": fmt.Sprintf(format, args...)})
}

// reportParams are the request-scoped knobs that used to be per-process CLI
// flags and env vars.
type reportParams struct {
	format     report.Format
	truthOn    bool
	snapshotOn bool
	live       bool
	noCache    bool
	opts       source.Options
}

func (s *Server) parseReportParams(r *http.Request) (reportParams, error) {
	q := r.URL.Query()
	p := reportParams{format: report.FormatNDJSON, truthOn: true, snapshotOn: true}
	if v := q.Get("format"); v != "" {
		f, err := report.ParseFormat(v)
		if err != nil {
			return p, err
		}
		p.format = f
	}
	switch q.Get("truth") {
	case "", "on":
	case "off":
		p.truthOn = false
	default:
		return p, fmt.Errorf("invalid truth %q (use on or off)", q.Get("truth"))
	}
	// snapshot is independent of truth: paid reports disable the second-hand
	// zoning mirror (truth=off) but still use first-party dataset snapshots.
	switch q.Get("snapshot") {
	case "", "on":
	case "off":
		p.snapshotOn = false
	default:
		return p, fmt.Errorf("invalid snapshot %q (use on or off)", q.Get("snapshot"))
	}
	p.live = boolParam(q.Get("live"))
	p.noCache = boolParam(q.Get("no_cache"))

	opts := s.baseOpts
	opts.Live = p.live
	if p.noCache {
		opts.Cache = nil
	}
	if !p.truthOn {
		opts.TruthAPI = ""
	}
	if !p.snapshotOn {
		opts.SnapshotAPI = ""
	}
	if v := q.Get("attempt_timeout_s"); v != "" {
		seconds, err := strconv.ParseFloat(v, 64)
		if err != nil || seconds <= 0 {
			return p, fmt.Errorf("invalid attempt_timeout_s %q", v)
		}
		opts.AttemptTimeout = time.Duration(seconds * float64(time.Second))
	}
	if v := q.Get("max_attempts"); v != "" {
		attempts, err := strconv.Atoi(v)
		if err != nil || attempts <= 0 {
			return p, fmt.Errorf("invalid max_attempts %q", v)
		}
		opts.MaxAttempts = attempts
	}
	p.opts = opts
	return p, nil
}

func boolParam(v string) bool {
	return v == "1" || v == "true"
}

func (s *Server) engine(opts source.Options) *query.Engine {
	eng := query.New(s.resolver, opts)
	if s.freguesias != nil {
		eng.SetFreguesias(s.freguesias)
	}
	return eng
}

// acquire takes a query slot, honoring the request context while waiting.
func (s *Server) acquire(ctx context.Context) bool {
	select {
	case s.sem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *Server) release() { <-s.sem }

func (s *Server) handlePointReport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	lat, errLat := strconv.ParseFloat(q.Get("lat"), 64)
	lon, errLon := strconv.ParseFloat(q.Get("lon"), 64)
	if errLat != nil || errLon != nil {
		writeErr(w, http.StatusUnprocessableEntity, "report needs numeric lat and lon")
		return
	}
	if err := spatial.ValidateLatLon(lat, lon); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "%v", err)
		return
	}
	p, err := s.parseReportParams(r)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "%v", err)
		return
	}
	if !s.acquire(r.Context()) {
		return // client gone while waiting for a slot
	}
	defer s.release()

	ctx, cancel := context.WithTimeout(r.Context(), queryBudget)
	defer cancel()
	eng := s.engine(p.opts)

	if p.format == report.FormatNDJSON {
		s.streamReport(w,
			func(emit query.Emit) (any, error) { return eng.PointStream(ctx, lon, lat, emit) },
			nil) // PointStream reuses its zoning geometry to emit the locator map.
		return
	}

	res, err := eng.Point(ctx, lon, lat)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	var buf bytes.Buffer
	if p.format == report.FormatHTML {
		svg := ""
		if m := eng.PointMap(ctx, lon, lat); m != nil {
			svg = m.SVG()
		}
		err = report.PointHTML(&buf, res, svg)
	} else {
		err = report.Point(&buf, res, p.format)
	}
	s.finishBuffered(w, p.format, &buf, err)
}

func (s *Server) handlePolygonReport(w http.ResponseWriter, r *http.Request) {
	p, err := s.parseReportParams(r)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "%v", err)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "reading body: %v", err)
		return
	}
	g, err := spatial.ParseInputGeometry(body)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "%v", err)
		return
	}
	if !s.acquire(r.Context()) {
		return
	}
	defer s.release()

	ctx, cancel := context.WithTimeout(r.Context(), queryBudget)
	defer cancel()
	eng := s.engine(p.opts)

	if p.format == report.FormatNDJSON {
		s.streamReport(w,
			func(emit query.Emit) (any, error) { return eng.PolygonStream(ctx, g, emit) },
			func() *mapview.Data { return polygonMap(eng, g) })
		return
	}

	res, err := eng.Polygon(ctx, g)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	var buf bytes.Buffer
	if p.format == report.FormatHTML {
		svg := ""
		if m := eng.PolygonMap(ctx, g); m != nil {
			svg = m.SVG()
		}
		err = report.PolygonHTML(&buf, res, svg)
	} else {
		err = report.Polygon(&buf, res, p.format)
	}
	s.finishBuffered(w, p.format, &buf, err)
}

// polygonMap renders the locator map on its own budget, like the CLI stream
// path does — a slow basemap must not stall the report stream's terminal event
// beyond this window.
func polygonMap(eng *query.Engine, g geom.Geometry) *mapview.Data {
	mapCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return eng.PolygonMap(mapCtx, g)
}

// streamReport wires query.StreamNDJSON to the response, flushing per event so
// the browser renders progressively. Status is committed as 200 before the
// engine runs: engine failures surface as the terminal error event, exactly
// like a spawned `pdm --format ndjson` does on its stdout.
func (s *Server) streamReport(w http.ResponseWriter, run func(query.Emit) (any, error), buildMap func() *mapview.Data) {
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fw := flushWriter{w: w}
	if f, ok := w.(http.Flusher); ok {
		fw.f = f
	}
	_ = query.StreamNDJSON(fw, run, buildMap) // error already emitted as the terminal event
}

func (s *Server) finishBuffered(w http.ResponseWriter, format report.Format, buf *bytes.Buffer, err error) {
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	switch format {
	case report.FormatJSON:
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	case report.FormatHTML:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	default:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

// flushWriter flushes after every write; the NDJSON emitter writes exactly one
// event per Write call, so this flushes per event.
type flushWriter struct {
	w io.Writer
	f http.Flusher
}

func (fw flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if fw.f != nil {
		fw.f.Flush()
	}
	return n, err
}
