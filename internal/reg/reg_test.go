package reg

import "testing"

const sample = `{
  "reference": "Aviso X",
  "_source": {"url": "https://example/reg.pdf"},
  "articles": [
    {"number":"31.º","title":"Identificação e usos","section":"Espaços Centrais","text":"..."},
    {"number":"32.º","title":"Espaços Centrais Nível 1 — regime de edificabilidade","section":"Espaços Centrais","text":"..."},
    {"number":"35.º","title":"Identificação e usos","section":"Espaços Habitacionais","text":"..."},
    {"number":"52.º","title":"Regime geral","section":"Espaços Agrícolas","text":"..."}
  ]
}`

func load(t *testing.T) *Store {
	t.Helper()
	s, err := Load([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMatchSectionSingularPlural(t *testing.T) {
	s := load(t)
	// CRUS category "Espaço Central" (singular) must match section "Espaços
	// Centrais" (plural).
	got := s.MatchSection("Espaço Central")
	if len(got) != 2 {
		t.Fatalf("expected 2 Espaços Centrais articles, got %d", len(got))
	}
	if got[0].Number != "31.º" {
		t.Errorf("wrong first article: %s", got[0].Number)
	}
}

func TestMatchSectionHabitacionais(t *testing.T) {
	s := load(t)
	got := s.MatchSection("Solo Urbano – Espaços Habitacionais")
	if len(got) != 1 || got[0].Number != "35.º" {
		t.Fatalf("expected only Art 35, got %v", got)
	}
}

func TestMatchSectionNoFalsePositive(t *testing.T) {
	s := load(t)
	// Agrícola must not pull in Centrais/Habitacionais.
	got := s.MatchSection("Espaços Agrícolas de Produção")
	if len(got) != 1 || got[0].Number != "52.º" {
		t.Fatalf("expected only the Agrícolas article, got %v", got)
	}
}

func TestMatchSectionMeta(t *testing.T) {
	s := load(t)
	if s.Reference != "Aviso X" || s.URL != "https://example/reg.pdf" {
		t.Errorf("meta not loaded: ref=%q url=%q", s.Reference, s.URL)
	}
}
