package report

import "testing"

func TestGroupThousands(t *testing.T) {
	cases := map[int64]string{
		0:       "0",
		78:      "78",
		3250:    "3,250",
		9537565: "9,537,565",
		-1234:   "-1,234",
	}
	for in, want := range cases {
		if got := group(in); got != want {
			t.Errorf("group(%d)=%q, want %q", in, got, want)
		}
	}
}

func TestArea(t *testing.T) {
	if got := area(3337.4); got != "3,337" {
		t.Errorf("area rounding: %q", got)
	}
}

func TestPct(t *testing.T) {
	if got := pct(64.63); got != "64.6" {
		t.Errorf("pct=%q", got)
	}
	if got := pct(100); got != "100.0" {
		t.Errorf("pct=%q", got)
	}
}

func TestParseFormat(t *testing.T) {
	for _, s := range []string{"", "text", "json", "markdown", "md"} {
		if _, err := ParseFormat(s); err != nil {
			t.Errorf("ParseFormat(%q) errored: %v", s, err)
		}
	}
	if _, err := ParseFormat("xml"); err == nil {
		t.Error("ParseFormat(xml) should error")
	}
}
