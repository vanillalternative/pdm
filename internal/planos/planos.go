// Package planos serves the bundled national registry of special planning
// instruments (planos/programas especiais de ordenamento do território):
// reservoir plans (POAAP/PEAAP), coastal plans and programs (POOC/POC),
// estuary plans (POE — none ever approved), protected-area plans (POAP/PEAP)
// and the Natura 2000 sectoral plan. The registry answers "which special
// instruments touch this municipality", regardless of whether their geometry
// is available anywhere — the live layers then confirm, when they can, that
// the queried location falls inside an instrument's area.
//
// Special plans still matter after the 2015 reform: a plano especial whose
// norms were not transposed into the PDM by 13 Jul 2021 no longer binds
// private parties directly, but it remains in force, suspends incompatible
// municipal norms and keeps binding the licensing entities (APA, ICNF, CCDR) —
// so a query tool must keep surfacing them per municipality.
package planos

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/bernardosimoes/pdm/data"
	"github.com/bernardosimoes/pdm/internal/model"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// instrument mirrors one registry entry (superset of model.Instrument: the
// subjects match-keys stay internal).
type instrument struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Kind           string   `json:"kind"`
	Family         string   `json:"family"`
	Diploma        string   `json:"diploma"`
	Status         string   `json:"status"`
	State          string   `json:"state"`
	Municipalities []string `json:"municipalities"`
	Notes          string   `json:"notes"`
	Subjects       []string `json:"subjects"`
}

var (
	once   sync.Once
	byMuni map[string][]*instrument
	byID   map[string]*instrument
	count  int
)

func load() {
	byMuni = map[string][]*instrument{}
	byID = map[string]*instrument{}
	var doc struct {
		Instruments []*instrument `json:"instruments"`
	}
	if err := json.Unmarshal(data.Planos, &doc); err != nil {
		return
	}
	for _, ins := range doc.Instruments {
		byID[ins.ID] = ins
		for _, m := range ins.Municipalities {
			key := normalize(m)
			byMuni[key] = append(byMuni[key], ins)
		}
	}
	count = len(doc.Instruments)
}

// Count returns the number of instruments in the registry.
func Count() int {
	once.Do(load)
	return count
}

// stateRank orders instruments by how much they matter right now.
var stateRank = map[string]int{
	"vigor": 0, "parcial": 1, "elaboracao": 2, "revogado": 3, "nunca-aprovado": 4,
}

// ForMunicipality returns the special instruments touching the named
// municipality, most-relevant state first. PointInside is left nil — the
// caller enriches it from live layer hits via Match.
func ForMunicipality(name string) []model.Instrument {
	once.Do(load)
	list := byMuni[normalize(name)]
	if len(list) == 0 {
		return nil
	}
	out := make([]model.Instrument, 0, len(list))
	for _, ins := range list {
		out = append(out, model.Instrument{
			ID:             ins.ID,
			Name:           ins.Name,
			Kind:           ins.Kind,
			Family:         ins.Family,
			Diploma:        ins.Diploma,
			Status:         ins.Status,
			State:          ins.State,
			Municipalities: ins.Municipalities,
			Notes:          ins.Notes,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := stateRank[out[i].State], stateRank[out[j].State]
		if ri != rj {
			return ri < rj
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// subjects returns the internal match-keys for an instrument id.
func subjects(id string) []string {
	once.Do(load)
	if ins, ok := byID[id]; ok {
		return ins.Subjects
	}
	return nil
}

// Match reports whether the instrument (by id) is named by the given live
// detail text. Both sides are tokenized (lowercase, accent-stripped, Portuguese
// connective stopwords dropped) and a subject only matches as a contiguous
// token sequence — so "Vilar" never matches "Vilarinho das Furnas".
func Match(id, detail string) bool {
	dt := Tokens(detail)
	if len(dt) == 0 {
		return false
	}
	for _, s := range subjects(id) {
		st := strings.Fields(s)
		if len(st) > 0 && containsSeq(dt, st) {
			return true
		}
	}
	return false
}

func containsSeq(haystack, needle []string) bool {
	if len(needle) > len(haystack) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		ok := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

var stopwords = map[string]bool{
	"de": true, "do": true, "da": true, "dos": true, "das": true,
	"e": true, "d": true, "a": true, "o": true,
}

// Tokens folds text for subject matching: lowercase, accents stripped,
// non-alphanumerics split, connective stopwords dropped. Mirrors the registry
// generator exactly.
func Tokens(s string) []string {
	folded := normalize(s)
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			w := b.String()
			if !stopwords[w] {
				out = append(out, w)
			}
			b.Reset()
		}
	}
	for _, r := range folded {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

// normalize folds to lowercase and strips diacritics (same folding as the
// municipality registry, kept local to avoid an import cycle).
func normalize(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, err := transform.String(t, s)
	if err != nil {
		return s
	}
	return out
}
