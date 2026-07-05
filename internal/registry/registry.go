// Package registry maps a municipality name to its Adapter. Only municipalities
// with a registered adapter are "supported"; everything else resolves to a
// clear "detected but not yet supported" outcome.
package registry

import (
	"sort"
	"strings"
	"unicode"

	"github.com/bernardosimoes/pdm/internal/adapter"
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

// Lookup returns the adapter serving the named municipality, if supported.
func Lookup(name string) (adapter.Adapter, bool) {
	a, ok := adapters[Normalize(name)]
	return a, ok
}

// Supported returns the sorted list of supported municipality names.
func Supported() []string {
	out := make([]string, 0, len(adapters))
	for _, a := range adapters {
		out = append(out, a.Municipality())
	}
	sort.Strings(out)
	return out
}
