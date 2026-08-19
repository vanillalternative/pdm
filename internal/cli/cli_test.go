package cli

import (
	"bytes"
	"strings"
	"testing"

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

func TestRunSwappedCoordsRejected(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"point", "-8.41", "39.60"}, &out, &errb)
	if code == 0 {
		t.Errorf("swapped lat/lon should be rejected, got exit 0")
	}
}
