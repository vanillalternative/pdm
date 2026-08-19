package planos

import (
	"strings"
	"testing"
)

func TestRegistryLoads(t *testing.T) {
	if n := Count(); n < 100 {
		t.Fatalf("expected the full national registry (~121 instruments), got %d", n)
	}
}

func TestForMunicipality(t *testing.T) {
	tomar := ForMunicipality("Tomar")
	var hasPOACB, hasPEACB bool
	for _, i := range tomar {
		if strings.HasPrefix(i.Name, "POACB") {
			hasPOACB = true
			if i.State != "vigor" {
				t.Errorf("POACB should be em vigor, got %s", i.State)
			}
		}
		if strings.HasPrefix(i.Name, "PEACB") {
			hasPEACB = true
			if i.State != "elaboracao" {
				t.Errorf("PEACB should be em elaboração, got %s", i.State)
			}
		}
	}
	if !hasPOACB || !hasPEACB {
		t.Fatalf("Tomar must list POACB and PEACB, got %+v", tomar)
	}
	// Case- and accent-insensitive municipality lookup.
	if len(ForMunicipality("TOMAR")) != len(tomar) {
		t.Error("municipality lookup must be case-insensitive")
	}
	if len(ForMunicipality("Ourém")) == 0 || len(ForMunicipality("ourem")) == 0 {
		t.Error("accented and folded municipality names must both resolve")
	}
	// Ordering: in-force instruments come before pipeline ones.
	if tomar[0].State != "vigor" {
		t.Errorf("in-force instruments must sort first, got %s", tomar[0].State)
	}
}

func TestMatchTokenSequences(t *testing.T) {
	// Find real registry ids to match against.
	var poacb, vilar string
	for id, ins := range byIDForTest() {
		if strings.HasPrefix(ins.Name, "POACB") {
			poacb = id
		}
		if ins.Name == "POA da Albufeira do Vilar" {
			vilar = id
		}
	}
	if poacb == "" || vilar == "" {
		t.Fatal("expected POACB and POA do Vilar in the registry")
	}
	// The APA layer says "Castelo de Bode", the registry "Castelo do Bode" —
	// stopword folding makes them equal.
	if !Match(poacb, "sobre o plano de água da albufeira de Castelo de Bode (classificação: Protegida)") {
		t.Error("POACB must match the live albufeira detail")
	}
	// "Vilar" must NOT match "Vilarinho das Furnas" — token sequences, not substrings.
	if Match(vilar, "sobre o plano de água da albufeira de Vilarinho das Furnas") {
		t.Error("Vilar must not substring-match Vilarinho das Furnas")
	}
	if !Match(vilar, "albufeira do Vilar") {
		t.Error("Vilar must match its own name")
	}
}

// byIDForTest exposes the internal index for test assertions.
func byIDForTest() map[string]*instrument {
	once.Do(load)
	return byID
}
