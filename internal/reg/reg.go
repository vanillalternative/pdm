// Package reg holds a plan's written regulation (Regulamento) parsed into
// articles, and retrieves the articles applicable to a zoning category by
// matching the category name against the article's section. This is pure
// retrieval — it surfaces the regulation for a human or AI to read; it does not
// interpret or extract building parameters.
package reg

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/bernardosimoes/pdm/internal/model"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Store is a parsed regulation document.
type Store struct {
	Reference string
	URL       string
	Articles  []model.Article
}

// Load parses a regulamento.json document (as produced by cmd/pdmdata).
func Load(data []byte) (*Store, error) {
	var doc struct {
		Reference string `json:"reference"`
		Source    struct {
			URL string `json:"url"`
		} `json:"_source"`
		Articles []model.Article `json:"articles"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse regulamento: %w", err)
	}
	return &Store{Reference: doc.Reference, URL: doc.Source.URL, Articles: doc.Articles}, nil
}

// MatchSection returns the articles whose section title shares a discriminating
// term with any of the given zoning-category terms (e.g. "Espaço Central" →
// section "Espaços Centrais"). Order and de-duplication are preserved.
func (s *Store) MatchSection(terms ...string) []model.Article {
	want := map[string]bool{}
	for _, t := range terms {
		for tok := range tokens(t) {
			want[tok] = true
		}
	}
	if len(want) == 0 {
		return nil
	}
	var out []model.Article
	for _, a := range s.Articles {
		if a.Section == "" {
			continue
		}
		for tok := range tokens(a.Section) {
			if want[tok] {
				out = append(out, a)
				break
			}
		}
	}
	return out
}

// stopwords are the generic planning words that carry no discriminating meaning
// between categories, so they are excluded from matching.
var stopwords = map[string]bool{
	"espaco": true, "de": true, "do": true, "da": true, "e": true, "em": true,
	"solo": true, "urbano": true, "rustico": true, "nivel": true, "regime": true,
	"edificabilidade": true, "identificacao": true, "usos": true, "uso": true,
	"i": true, "ii": true, "iii": true, "iv": true, "os": true, "as": true,
}

// tokens normalises a title/category to its set of discriminating, singularised
// content tokens.
func tokens(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strip(s), func(r rune) bool {
		return !unicode.IsLetter(r)
	}) {
		w = singularise(w)
		if w == "" || stopwords[w] || len(w) < 3 {
			continue
		}
		out[w] = true
	}
	return out
}

// singularise crudely maps common Portuguese plurals to a stable stem so
// "centrais"→"central", "habitacionais"→"habitacional", "verdes"→"verde".
func singularise(w string) string {
	switch {
	case strings.HasSuffix(w, "ais") && len(w) > 4:
		return w[:len(w)-3] + "al"
	case strings.HasSuffix(w, "eis") && len(w) > 4:
		return w[:len(w)-3] + "el"
	case strings.HasSuffix(w, "ois") && len(w) > 4:
		return w[:len(w)-3] + "ol"
	case strings.HasSuffix(w, "es") && len(w) > 4:
		return w[:len(w)-2]
	case strings.HasSuffix(w, "s") && len(w) > 3:
		return w[:len(w)-1]
	}
	return w
}

// strip lowercases and removes diacritics.
func strip(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, err := transform.String(t, s)
	if err != nil {
		return s
	}
	return out
}
