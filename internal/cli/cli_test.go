package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bernardosimoes/pdm/internal/ai"
	"github.com/bernardosimoes/pdm/internal/report"
)

func TestIsFlagAcceptsNegativeNumbers(t *testing.T) {
	cases := map[string]bool{
		"-8.41":    false, // negative longitude — positional
		"-.5":      false,
		"39.6":     false,
		"-":        false,
		"--format": true,
		"-f":       true,
		"--live":   true,
		"point":    false,
	}
	for in, want := range cases {
		if got := isFlag(in); got != want {
			t.Errorf("isFlag(%q)=%v, want %v", in, got, want)
		}
	}
}

func TestParseSeparatesFlagsAndPositionals(t *testing.T) {
	var o options
	pos, err := parse([]string{"point", "39.60", "-8.41", "--format", "json"}, &o)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(pos, ",") != "point,39.60,-8.41" {
		t.Errorf("positionals wrong: %v", pos)
	}
	if o.format != report.FormatJSON {
		t.Errorf("format not parsed: %v", o.format)
	}
}

func TestParseInlineFlagValue(t *testing.T) {
	var o options
	pos, err := parse([]string{"--format=markdown", "x.geojson"}, &o)
	if err != nil {
		t.Fatal(err)
	}
	if o.format != report.FormatMarkdown || len(pos) != 1 || pos[0] != "x.geojson" {
		t.Errorf("inline flag parse failed: fmt=%v pos=%v", o.format, pos)
	}
}

func TestParseUnknownFlag(t *testing.T) {
	var o options
	if _, err := parse([]string{"--nope"}, &o); err == nil {
		t.Error("unknown flag should error")
	}
}

func TestRunVersion(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"version"}, &out, &errb)
	if code != 0 || !strings.Contains(out.String(), "pdm ") {
		t.Errorf("version failed: code=%d out=%q", code, out.String())
	}
}

func TestRunPointIntegration(t *testing.T) {
	// End-to-end through the CLI against the bundled Tomar data (offline).
	var out, errb bytes.Buffer
	code := Run([]string{"39.60", "-8.41"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d, stderr=%q", code, errb.String())
	}
	if !strings.Contains(out.String(), "Tomar") {
		t.Errorf("expected Tomar in output, got:\n%s", out.String())
	}
}

func TestRunSupported(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"supported"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d, stderr=%q", code, errb.String())
	}
	for _, want := range []string{"Tomar", "CRUS", "mainland"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("supported output missing %q:\n%s", want, out.String())
		}
	}
}

// TestRunMunicipalitiesContract guards the JSON shape web/server.js parses at
// boot (it exits the process if this command fails or drifts). Field names
// are a wire contract — never rename them.
func TestRunMunicipalitiesContract(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"municipalities"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d, stderr=%q", code, errb.String())
	}
	var doc struct {
		Count          int `json:"count"`
		Municipalities []struct {
			Name     string `json:"name"`
			Code     string `json:"code"`
			District string `json:"district"`
			Centroid *struct {
				Lat float64 `json:"lat"`
				Lon float64 `json:"lon"`
			} `json:"centroid"`
			BBox        []float64 `json:"bbox"`
			Regulamento *struct {
				Reference string `json:"reference"`
				URL       string `json:"url"`
				Articles  int    `json:"articles"`
				Status    string `json:"status"`
			} `json:"regulamento"`
			SpecialPlans []struct {
				Name string `json:"name"`
				Kind string `json:"kind"`
			} `json:"special_plans"`
		} `json:"municipalities"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("municipalities output is not the expected JSON: %v", err)
	}
	if doc.Count < 270 || len(doc.Municipalities) != doc.Count {
		t.Fatalf("count = %d, entries = %d", doc.Count, len(doc.Municipalities))
	}
	var tomar bool
	for _, m := range doc.Municipalities {
		if m.Name == "" || m.Code == "" || m.District == "" {
			t.Fatalf("entry missing name/code/district: %+v", m)
		}
		if m.Centroid == nil || len(m.BBox) != 4 {
			t.Fatalf("entry %s missing centroid/bbox", m.Name)
		}
		if m.Code == "1418" {
			tomar = true
			if m.Regulamento == nil || m.Regulamento.Articles == 0 || m.Regulamento.Reference == "" {
				t.Errorf("Tomar must carry its parsed regulamento, got %+v", m.Regulamento)
			}
			if len(m.SpecialPlans) == 0 {
				t.Errorf("Tomar must list its special plans (POACB), got none")
			}
		}
	}
	if !tomar {
		t.Error("dtcc 1418 (Tomar) missing from the municipalities list")
	}
}

// TestRunReportNDJSONEventOrder guards the streaming contract web/server.js
// relays to the browser: the first event is meta, the terminal event is
// result, and every line is a standalone JSON object. Fetch budgets are
// pinned tiny so probe layers fail fast offline — failures surface as layer
// events, never as a broken stream.
func TestRunReportNDJSONEventOrder(t *testing.T) {
	t.Setenv("PDM_FETCH_TIMEOUT_SECONDS", "2")
	t.Setenv("PDM_FETCH_MAX_ATTEMPTS", "1")
	var out, errb bytes.Buffer
	code := Run([]string{"report", "39.60", "-8.41", "--format", "ndjson", "--no-cache"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d, stderr=%q", code, errb.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least meta+layer+result events, got %d lines", len(lines))
	}
	events := make([]string, len(lines))
	for i, l := range lines {
		var ev struct {
			Event string `json:"event"`
		}
		if err := json.Unmarshal([]byte(l), &ev); err != nil {
			t.Fatalf("line %d is not JSON: %v\n%s", i, err, l)
		}
		events[i] = ev.Event
	}
	if events[0] != "meta" {
		t.Errorf("first event = %q, want meta", events[0])
	}
	if events[len(events)-1] != "result" {
		t.Errorf("terminal event = %q, want result", events[len(events)-1])
	}
	for _, e := range events[1 : len(events)-1] {
		if e == "meta" || e == "result" {
			t.Errorf("unexpected mid-stream %q event in %v", e, events)
		}
	}
}

func TestRunSwappedCoordsRejected(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"point", "-8.41", "39.60"}, &out, &errb)
	if code == 0 {
		t.Errorf("swapped lat/lon should be rejected, got exit 0")
	}
}

// fakeGenerator satisfies ai.Generator for offline CLI tests.
type fakeGenerator struct {
	analysis *ai.Analysis
	err      error
	sawKind  string
}

func (f *fakeGenerator) Generate(_ context.Context, p ai.Payload) (*ai.Analysis, error) {
	f.sawKind = p.Kind
	return f.analysis, f.err
}

func stubGenerator(t *testing.T, fake *fakeGenerator) {
	t.Helper()
	orig := newGenerator
	newGenerator = func(ai.Tier) (ai.Generator, error) { return fake, nil }
	t.Cleanup(func() { newGenerator = orig })
}

func TestRunAnalyseStubbed(t *testing.T) {
	fake := &fakeGenerator{analysis: &ai.Analysis{
		Title:            "Análise urbanística — Tomar",
		ExecutiveSummary: "Resumo executivo de teste.",
		ViabilitySignal:  ai.ViabilityCondicionado,
		Sections:         []ai.Section{{ID: "identificacao", Heading: "Identificação", Reading: "Prosa de teste.", Notes: ""}},
	}}
	stubGenerator(t, fake)
	var out, errb bytes.Buffer
	code := Run([]string{"analyse", "39.60", "-8.41", "--format", "markdown", "--tier", "premium"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d, stderr=%q", code, errb.String())
	}
	if fake.sawKind != "point" {
		t.Errorf("generator saw kind %q, want point", fake.sawKind)
	}
	for _, want := range []string{"Tomar", "Resumo executivo de teste.", "Prosa de teste."} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("analysis output missing %q", want)
		}
	}
}

func TestRunAnalyseDefaultsToHTML(t *testing.T) {
	fake := &fakeGenerator{analysis: &ai.Analysis{
		Title: "T", ExecutiveSummary: "S", ViabilitySignal: ai.ViabilityIndeterminado,
		Sections: []ai.Section{{ID: "identificacao", Heading: "H", Reading: "R"}},
	}}
	stubGenerator(t, fake)
	var out, errb bytes.Buffer
	if code := Run([]string{"analyse", "39.60", "-8.41"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d, stderr=%q", code, errb.String())
	}
	if !strings.Contains(out.String(), "<!doctype html>") {
		t.Error("analyse should default to HTML output")
	}
}

func TestRunAnalyseMissingKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	var out, errb bytes.Buffer
	if code := Run([]string{"analyse", "39.60", "-8.41"}, &out, &errb); code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(errb.String(), "ANTHROPIC_API_KEY") {
		t.Errorf("expected actionable key message, got %q", errb.String())
	}
}

func TestRunAnalyseFlagErrors(t *testing.T) {
	cases := map[string][]string{
		"bad tier":       {"analyse", "39.60", "-8.41", "--tier", "bogus"},
		"text rejected":  {"analyse", "39.60", "-8.41", "--format", "text"},
		"missing target": {"analyse"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			fake := &fakeGenerator{analysis: &ai.Analysis{}}
			stubGenerator(t, fake)
			var out, errb bytes.Buffer
			if code := Run(args, &out, &errb); code != 2 {
				t.Fatalf("expected exit 2, got %d (stderr=%q)", code, errb.String())
			}
		})
	}
}
