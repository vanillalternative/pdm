// Package registry maps a municipality to its Adapter. Municipalities with a
// registered dedicated adapter get full support (zoning + constraints +
// regulation); every other resolved municipality falls back to the generic
// adapter, which answers zoning from the national DGT CRUS dataset.
package registry

import (
	"sort"
	"strings"
	"unicode"

	"github.com/bernardosimoes/pdm/internal/adapter"
	"github.com/bernardosimoes/pdm/internal/adapter/generic"
	"github.com/bernardosimoes/pdm/internal/adapter/tomar"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

var adapters = map[string]adapter.Adapter{}

func register(a adapter.Adapter) {
	adapters[Normalize(a.Municipality())] = a
}

func init() {
	register(tomar.New())
}

// Normalize folds a municipality name for matching: trim, lowercase, and strip
// diacritics so "TOMAR", "Tomar", and "tomar" all match, and accented names
// coming from CAOP (e.g. "Ourém") compare cleanly.
func Normalize(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, err := transform.String(t, name)
	if err != nil {
		return name
	}
	return out
}

// Lookup returns the dedicated adapter serving the named municipality, if one
// is registered.
func Lookup(name string) (adapter.Adapter, bool) {
	a, ok := adapters[Normalize(name)]
	return a, ok
}

// Resolve returns the adapter serving a resolved municipality: the dedicated
// adapter when one is registered (dedicated=true), otherwise the generic
// CRUS-backed adapter, which answers zoning only (dedicated=false). code is
// the municipality's CAOP dtmn code.
func Resolve(name, code string) (a adapter.Adapter, dedicated bool) {
	if a, ok := Lookup(name); ok {
		return a, true
	}
	return generic.New(name, code), false
}

// Supported returns the sorted list of municipalities with a dedicated (full
// support) adapter.
func Supported() []string {
	out := make([]string, 0, len(adapters))
	for _, a := range adapters {
		out = append(out, a.Municipality())
	}
	sort.Strings(out)
	return out
}
