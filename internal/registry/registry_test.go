package registry

import "testing"

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"Tomar":         "tomar",
		"TOMAR":         "tomar",
		" tomar":        "tomar",
		"Ourém":         "ourem",
		"Idanha-a-Nova": "idanha-a-nova",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestLookup(t *testing.T) {
	if _, ok := Lookup("Tomar"); !ok {
		t.Error("Tomar should be supported")
	}
	if _, ok := Lookup("tomar"); !ok {
		t.Error("lookup should be case-insensitive")
	}
	if _, ok := Lookup("Lisboa"); ok {
		t.Error("Lisboa should not be supported")
	}
}

func TestSupportedContainsTomar(t *testing.T) {
	found := false
	for _, m := range Supported() {
		if m == "Tomar" {
			found = true
		}
	}
	if !found {
		t.Errorf("Supported() should list Tomar, got %v", Supported())
	}
}
