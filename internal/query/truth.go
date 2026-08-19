package query

import (
	"github.com/bernardosimoes/pdm/internal/adapter"
	"github.com/bernardosimoes/pdm/internal/model"
	"github.com/bernardosimoes/pdm/internal/source"
	"github.com/bernardosimoes/pdm/internal/spatial"
)

// mirrorNote is appended to any result whose zoning was answered by the pdms
// truth mirror instead of a fresh official fetch.
const mirrorNote = "Zonamento servido do espelho local pdms (dados oficiais registados anteriormente)."

// truthLayer decorates a generic zoning layer so the pdms truth mirror is
// consulted before the official source. It is a no-op unless the mirror is
// configured, the query is not --live, the municipality code is known, and the
// layer is a plain zoning loader (no probe). The caller has already confirmed
// the adapter is generic — mirror features carry recorded properties, which
// dedicated adapters' Classify functions would misread.
func truthLayer(l adapter.Layer, name, code string, opts source.Options) adapter.Layer {
	if opts.TruthAPI == "" || opts.Live || code == "" {
		return l
	}
	if l.Kind != adapter.KindZoning || l.Loader == nil || l.Probe != nil {
		return l
	}
	orig := l
	cfg := source.TruthConfig{
		BaseURL: opts.TruthAPI,
		Code:    code,
		Meta: model.Source{
			Name:  "pdms — espelho de zonamento registado — " + name,
			Layer: l.ID,
		},
	}
	l.Loader = source.Fallback(source.Truth(cfg, opts), orig.Loader)
	l.Classify = func(f spatial.Feature) adapter.Classification {
		if source.FromTruthMirror(f) {
			return classifyRecorded(f)
		}
		return classify(orig, f)
	}
	return l
}

// classifyRecorded reads a mirror feature's recorded classification verbatim.
// The mirror stores what the original classification produced, so no source
// schema (CRUS attribute names etc.) applies here.
func classifyRecorded(f spatial.Feature) adapter.Classification {
	class := f.Prop("class")
	sub := f.Prop("subclass")
	label := f.Prop("label")
	if label == "" {
		switch {
		case class != "" && sub != "":
			label = class + " — " + sub
		case sub != "":
			label = sub
		case class != "":
			label = class
		default:
			label = "(não classificado)"
		}
	}
	return adapter.Classification{Class: class, Subclass: sub, Label: label, RawCode: f.Prop("raw_code")}
}

// capForMirror is the honesty guard for mirror-served results: when any
// collected source is the recorded mirror, confidence is capped at medium
// (never raised) so a re-served answer never claims the trust of a fresh
// official fetch. mirrored reports whether the cap applied.
func capForMirror(sources []model.Source, c model.Confidence) (capped model.Confidence, mirrored bool) {
	for _, s := range sources {
		if s.Provenance == model.ProvenanceRecordedMirror {
			if c == model.ConfidenceHigh {
				c = model.ConfidenceMedium
			}
			return c, true
		}
	}
	return c, false
}
