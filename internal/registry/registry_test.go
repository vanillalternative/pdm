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
		t.Error("Tomar should have a dedicated adapter")
	}
	if _, ok := Lookup("tomar"); !ok {
		t.Error("lookup should be case-insensitive")
	}
	if _, ok := Lookup("Lisboa"); ok {
		t.Error("Lisboa should not have a dedicated adapter")
	}
}

func TestResolve(t *testing.T) {
	a, dedicated := Resolve("Tomar", "1418")
	if !dedicated || a.Municipality() != "Tomar" {
		t.Errorf("Tomar should resolve to the dedicated adapter, got %q/%v", a.Municipality(), dedicated)
	}
	g, dedicated := Resolve("Lisboa", "1106")
	if dedicated || g.Municipality() != "Lisboa" {
		t.Errorf("Lisboa should resolve to the generic adapter, got %q/%v", g.Municipality(), dedicated)
	}
	if g.Regulation() != nil {
		t.Error("generic adapter must not carry a regulation store")
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

func TestMafraHasDedicatedAdapter(t *testing.T) {
	a, dedicated := Resolve("Mafra", "1109")
	if !dedicated || a.Municipality() != "Mafra" {
		t.Fatalf("Mafra should resolve to its dedicated adapter, got %q/%v", a.Municipality(), dedicated)
	}
}
