package pombal

import (
	"testing"

	"github.com/bernardosimoes/pdm/internal/adapter"
	"github.com/bernardosimoes/pdm/internal/source"
	"github.com/bernardosimoes/pdm/internal/spatial"
)

func TestClassifyZoning(t *testing.T) {
	f := spatial.Feature{Props: map[string]any{
		"pdm_class": "Solo urbano",
		"descricao": "Espaços habitacionais",
		"objectid":  42,
	}}
	got := classifyZoning(f)
	if got.Class != "Solo urbano" || got.Subclass != "Espaços habitacionais" || got.Label != "Solo urbano — Espaços habitacionais" || got.RawCode != "42" {
		t.Fatalf("unexpected classification: %+v", got)
	}
}

func TestLayersExposeMunicipalDelimitations(t *testing.T) {
	layers := New().Layers(source.Options{})
	if len(layers) != 9 {
		t.Fatalf("got %d layers, want 9", len(layers))
	}
	if layers[0].Kind != adapter.KindZoning || layers[0].Loader == nil || layers[0].Classify == nil {
		t.Fatalf("first layer is not usable zoning: %+v", layers[0])
	}
	want := map[string]bool{"RAN": false, "REN": false, "Zona inundável": false, "Estrutura ecológica municipal": false}
	for _, layer := range layers[1:] {
		if _, ok := want[layer.Constraint]; ok {
			want[layer.Constraint] = true
		}
	}
	for constraint, found := range want {
		if !found {
			t.Errorf("missing municipal constraint %q", constraint)
		}
	}
}
