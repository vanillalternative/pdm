// Package national builds the constraint layers available for every mainland
// municipality from national services, without any per-municipality adapter
// work: RAN and REN from the DGT/SNIT SRUP collections, Rede Natura 2000 (ZEC
// and ZPE) and protected areas from the same catalogue, and the water/coastal
// special-plan layers (classified albufeiras, POC/POOC, POAAP/PAAP) from APA's
// SNIAmb ArcGIS services. All layers are server-side presence probes — the
// underlying polygons (whole-municipality REN delimitations, whole-park
// boundaries) are far too large to download per query, so the services are
// asked what intersects the subject and only attributes come back.
//
// Verified live against both services on 2026-07-06: the DGT OGC API evaluates
// bbox as a true geometry intersection (a RAN-yes point matches, a RAN-no point
// 300 m away does not), and the APA ArcGIS layers answer point and distance
// queries server-side.
package national

import (
	"context"
	"fmt"
	"strings"

	"github.com/bernardosimoes/pdm/internal/adapter"
	"github.com/bernardosimoes/pdm/internal/model"
	"github.com/bernardosimoes/pdm/internal/registry"
	"github.com/bernardosimoes/pdm/internal/source"
	"github.com/bernardosimoes/pdm/internal/spatial"
)

const (
	ogc = "https://ogcapi.dgterritorio.gov.pt/collections"
	apa = "https://sniambgeoogc.apambiente.pt/getogc/rest/services/SNIAmb"
)

// noSoloRustico are the municipalities that genuinely have no RAN because they
// have no rural land at all (per the national REOT), so an empty RAN dataset
// for them is a real "no", not a data gap.
var noSoloRustico = map[string]bool{
	"lisboa":  true,
	"porto":   true,
	"amadora": true,
}

// Layers returns the national constraint layers for a municipality. name and
// code identify it as resolved from CAOP (code is the dtmn/dtcc). Layers whose
// canonical constraint type is in skip are omitted — a dedicated adapter
// already provides them from a richer source.
func Layers(name, code string, opts source.Options, skip map[string]bool) []adapter.Layer {
	// Probes are always live: there is nothing bundled to fall back to, and
	// each response is a few KB, cached on disk.
	opts.Live = true

	var out []adapter.Layer
	add := func(l adapter.Layer) {
		if !skip[l.Constraint] {
			out = append(out, l)
		}
	}

	add(adapter.Layer{
		ID: "ran", Title: "RAN — Reserva Agrícola Nacional", Kind: adapter.KindConstraint,
		Constraint: "RAN",
		Probe:      ranProbe(name, opts),
	})
	add(adapter.Layer{
		ID: "ren", Title: "REN — Reserva Ecológica Nacional", Kind: adapter.KindConstraint,
		Constraint: "REN",
		Probe:      renProbe(name, code, opts),
	})
	add(adapter.Layer{
		ID: "natura-zec", Title: "Rede Natura 2000 — Sítio/ZEC (Diretiva Habitats)", Kind: adapter.KindConstraint,
		Constraint: "Rede Natura 2000 (ZEC)",
		Probe: source.OGCProbe(source.OGCProbeConfig{
			ItemsURL: ogc + "/srup_zec/items",
			Meta:     model.Source{Name: "DGT/SNIT — SRUP, Rede Natura 2000 (ZEC/Sítios)", Layer: "srup_zec"},
		}, opts),
	})
	add(adapter.Layer{
		ID: "natura-zpe", Title: "Rede Natura 2000 — Zona de Proteção Especial (Diretiva Aves)", Kind: adapter.KindConstraint,
		Constraint: "Rede Natura 2000 (ZPE)",
		Probe: source.OGCProbe(source.OGCProbeConfig{
			ItemsURL: ogc + "/srup_zpe/items",
			Meta:     model.Source{Name: "DGT/SNIT — SRUP, Rede Natura 2000 (ZPE)", Layer: "srup_zpe"},
		}, opts),
	})
	add(adapter.Layer{
		ID: "area-protegida", Title: "Área Protegida (RNAP)", Kind: adapter.KindConstraint,
		Constraint: "Área Protegida",
		Probe: source.OGCProbe(source.OGCProbeConfig{
			ItemsURL: ogc + "/srup_areas_protegidas/items",
			Interpret: func(hits []spatial.Feature) source.Interpretation {
				if len(hits) == 0 {
					return source.Interpretation{}
				}
				d := hits[0].Prop("designacao")
				if t := hits[0].Prop("tipologia"); t != "" {
					d += " (" + strings.TrimPrefix(t, "ÁREAS PROTEGIDAS - ") + ")"
				}
				return source.Interpretation{Present: true, Detail: d}
			},
			Meta: model.Source{Name: "DGT/SNIT — SRUP, Áreas Protegidas (RNAP)", Layer: "srup_areas_protegidas"},
		}, opts),
	})
	add(adapter.Layer{
		ID: "albufeira", Title: "Albufeira de águas públicas classificada (DL 107/2009)", Kind: adapter.KindConstraint,
		Constraint: "Albufeira classificada",
		Probe:      albufeiraProbe(opts),
	})
	add(adapter.Layer{
		ID: "orla-costeira", Title: "Orla costeira — área de intervenção POC/POOC", Kind: adapter.KindConstraint,
		Constraint: "Orla costeira",
		Probe:      coastProbe(opts),
	})
	add(adapter.Layer{
		ID: "faixa-salvaguarda", Title: "Faixas de salvaguarda costeira (POC)", Kind: adapter.KindConstraint,
		Constraint: "Faixa de salvaguarda costeira",
		Probe:      coastBandsProbe(opts),
	})
	add(adapter.Layer{
		ID: "poaap-zonamento", Title: "Plano de ordenamento de albufeira (POAAP) — zonamento", Kind: adapter.KindConstraint,
		Constraint: "Zonamento POAAP",
		Probe:      poaapProbe(opts),
	})
	add(adapter.Layer{
		ID: "paap", Title: "Programa especial de albufeira (PAAP) — área de intervenção", Kind: adapter.KindConstraint,
		Constraint: "Área de intervenção PAAP",
		Probe:      paapProbe(opts),
	})
	return out
}

// ranProbe answers RAN from the national SRUP collection: one multipolygon per
// municipality with a published carta da RAN. An existence check distinguishes
// "not in RAN" from "no RAN published for this municipality" (9 mainland
// municipalities are absent; Lisboa, Porto and Amadora legitimately so).
func ranProbe(name string, opts source.Options) source.Prober {
	cfg := source.OGCProbeConfig{
		ItemsURL: ogc + "/srup_ran/items",
		Interpret: func(hits []spatial.Feature) source.Interpretation {
			if len(hits) == 0 {
				return source.Interpretation{}
			}
			d := hits[0].Prop("designacao", "tipologia")
			if lei := hits[0].Prop("serv_lei"); lei != "" {
				d += " (" + lei + ")"
			}
			return source.Interpretation{Present: true, Detail: d}
		},
		Meta: model.Source{Name: "DGT/SNIT — SRUP, Reserva Agrícola Nacional (RAN)", Layer: "srup_ran"},
	}
	if noSoloRustico[registry.Normalize(name)] {
		// No existence check: the collection has no features for these three, and
		// that is a real answer (no rural land), not a data gap.
		base := source.OGCProbe(cfg, opts)
		return func(ctx context.Context, subject source.BBox) (source.Probed, error) {
			p, err := base(ctx, subject)
			if err == nil && !p.Present {
				p.Detail = "município sem solo rústico — não existe carta da RAN"
			}
			return p, err
		}
	}
	// The collection keys municipalities by their UPPERCASE ACCENTED name
	// (LOULÉ, not LOULE); CAOP names share the spelling, so uppercasing works.
	cfg.Existence = map[string]string{"municipio": strings.ToUpper(name)}
	cfg.AbsenceDetail = "carta da RAN não publicada no SNIT para este município"
	cfg.AbsenceNote = "A ausência de dados não significa ausência de RAN — confirmar junto da DGADR/CCDR."
	return source.OGCProbe(cfg, opts)
}

// renProbe answers REN from the national SRUP delimitation: per municipality
// the collection holds one whole-territory delimitation multipolygon plus, for
// most, an exclusions multipolygon that must be SUBTRACTED — a point hitting
// an exclusion is out of the REN even though it is inside the delimitation.
// 50 mainland municipalities are missing from the collection (their REN exists
// under older regimes but is not loaded in SNIT), so an existence check keeps
// the answer honest there.
func renProbe(name, code string, opts source.Options) source.Prober {
	cfg := source.OGCProbeConfig{
		ItemsURL: ogc + "/srup_ren_areal/items",
		Interpret: func(hits []spatial.Feature) source.Interpretation {
			var ren, excl bool
			var lei string
			for _, h := range hits {
				t := registry.Normalize(h.Prop("tipologia"))
				switch {
				case strings.Contains(t, "exclus"):
					excl = true
				case strings.Contains(t, "reserva ecologica"):
					ren = true
					if lei == "" {
						lei = h.Prop("serv_lei")
					}
				}
			}
			// The delimitation multipolygon already has the exclusion areas
			// carved out geometrically, so a subject normally hits one or the
			// other. Hitting BOTH means the subject envelope straddles the line
			// (a parcel, or a point on the boundary) — part of it is in the REN,
			// so the honest answer is present, flagged as partial.
			switch {
			case ren && excl:
				d := "delimitação municipal da REN em vigor"
				if lei != "" {
					d += " (" + lei + ")"
				}
				return source.Interpretation{
					Present: true,
					Detail:  d + " — o envelope consultado abrange também uma área de exclusão (cobertura parcial)",
					Note:    "A fonte nacional dá a delimitação da REN sem desagregação por tipologia (erosão, cheias, aquíferos…).",
				}
			case excl:
				return source.Interpretation{
					Present: false,
					Detail:  "área de exclusão da REN (dentro da delimitação municipal, mas excluída)",
				}
			case ren:
				d := "delimitação municipal da REN em vigor"
				if lei != "" {
					d += " (" + lei + ")"
				}
				return source.Interpretation{
					Present: true,
					Detail:  d,
					Note:    "A fonte nacional dá a delimitação da REN sem desagregação por tipologia (erosão, cheias, aquíferos…).",
				}
			default:
				return source.Interpretation{}
			}
		},
		Meta: model.Source{Name: "DGT/SNIT — SRUP, Reserva Ecológica Nacional (REN)", Layer: "srup_ren_areal"},
	}
	if code != "" {
		cfg.Params = map[string]string{"dtcc": code}
		cfg.Existence = map[string]string{"dtcc": code}
	} else {
		cfg.Existence = map[string]string{"municipio": strings.ToUpper(name)}
	}
	cfg.AbsenceDetail = "delimitação da REN não disponível no SNIT para este município"
	cfg.AbsenceNote = "A REN deste município existe mas não está carregada na fonte nacional (SNIT) — " +
		"a ausência de dados não significa ausência de REN; confirmar junto da CCDR ou do geoportal municipal."
	return source.OGCProbe(cfg, opts)
}

// albufeiraProbe answers "is the subject on, or within the statutory
// protection belt of, a classified albufeira de águas públicas" from APA's
// national water-body layer. DL 107/2009 gives every classified albufeira a
// 500 m zona terrestre de proteção (1000 m for Alqueva) measured from the full
// storage level, with a 100 m zona reservada inside it — the belt checks are
// true server-side distance queries, not buffers computed here.
func albufeiraProbe(opts source.Options) source.Prober {
	layer := apa + "/Albufeiras_AguasPublicas/MapServer/0"
	fields := []string{"nome", "classifica", "poaap", "legislacao"}
	inWater := source.ArcGISProbe(source.ArcGISProbeConfig{
		LayerURL: layer, OutFields: fields,
		Meta: model.Source{Name: "APA/SNIAmb — Albufeiras de águas públicas classificadas", Layer: "albufeiras"},
	}, opts)
	near500 := source.ArcGISProbe(source.ArcGISProbeConfig{
		LayerURL: layer, OutFields: fields, DistanceM: 500,
		Meta: model.Source{Name: "APA/SNIAmb — Albufeiras de águas públicas classificadas", Layer: "albufeiras"},
	}, opts)
	near1000Alqueva := source.ArcGISProbe(source.ArcGISProbeConfig{
		LayerURL: layer, OutFields: fields, DistanceM: 1000,
		Where: "UPPER(nome) LIKE '%ALQUEVA%'",
		Meta:  model.Source{Name: "APA/SNIAmb — Albufeiras de águas públicas classificadas", Layer: "albufeiras"},
	}, opts)

	describe := func(h spatial.Feature) string {
		d := h.Prop("nome")
		if c := h.Prop("classifica"); c != "" {
			d += " (classificação: " + c + ")"
		}
		return d
	}
	return func(ctx context.Context, subject source.BBox) (source.Probed, error) {
		hits, src, err := inWater(ctx, subject)
		if err != nil {
			return source.Probed{}, err
		}
		if len(hits) > 0 {
			return source.Probed{
				Present: true,
				Detail:  "sobre o plano de água da albufeira de " + describe(hits[0]),
				Note:    "Aplicam-se o regime do DL 107/2009 e, quando exista, o plano/programa especial da albufeira.",
				Source:  src,
			}, nil
		}
		hits, src, err = near500(ctx, subject)
		if err != nil {
			return source.Probed{}, err
		}
		if len(hits) > 0 {
			return source.Probed{
				Present: true,
				Detail:  "a menos de 500 m do plano de água da albufeira de " + describe(hits[0]) + " — zona terrestre de proteção estatutária (DL 107/2009)",
				Note:    "Distância medida ao nível de pleno armazenamento; dentro da zona terrestre de proteção a faixa dos primeiros 100 m é a zona reservada.",
				Source:  src,
			}, nil
		}
		hits, src, err = near1000Alqueva(ctx, subject)
		if err != nil {
			return source.Probed{}, err
		}
		if len(hits) > 0 {
			return source.Probed{
				Present: true,
				Detail:  "a menos de 1000 m do plano de água da albufeira de " + describe(hits[0]) + " — zona terrestre de proteção do Alqueva",
				Source:  src,
			}, nil
		}
		return source.Probed{Source: src}, nil
	}
}

// coastProbe answers "which coastal plan/program area is the subject in" from
// APA's POC (programas, layer 0 = área de intervenção) and POOC (planos still
// in force, with zonamento) services.
func coastProbe(opts source.Options) source.Prober {
	poc := source.ArcGISProbe(source.ArcGISProbeConfig{
		LayerURL: apa + "/POC/MapServer/0", OutFields: []string{"poc", "legenda_1"},
		Meta: model.Source{Name: "APA/SNIAmb — Programas da Orla Costeira (POC)", Layer: "poc"},
	}, opts)
	pooc := source.ArcGISProbe(source.ArcGISProbeConfig{
		LayerURL: apa + "/POOC/MapServer/0", OutFields: []string{"nome", "local", "zonamento"},
		Meta: model.Source{Name: "APA/SNIAmb — Planos de Ordenamento da Orla Costeira (POOC)", Layer: "pooc"},
	}, opts)
	return func(ctx context.Context, subject source.BBox) (source.Probed, error) {
		var details []string
		hits, src, err := poc(ctx, subject)
		if err != nil {
			return source.Probed{}, err
		}
		for _, h := range hits {
			if n := h.Prop("poc"); n != "" {
				details = append(details, n+" — área de intervenção")
				break
			}
		}
		phits, psrc, err := pooc(ctx, subject)
		if err != nil {
			return source.Probed{}, err
		}
		seen := map[string]bool{}
		for _, h := range phits {
			n, z := h.Prop("nome"), h.Prop("zonamento")
			d := "POOC " + n
			if z != "" && !strings.EqualFold(z, "Área de intervenção do POOC") {
				d += ": " + z
			} else {
				d += " — área de intervenção"
			}
			if !seen[d] {
				seen[d] = true
				details = append(details, d)
			}
		}
		out := source.Probed{Present: len(details) > 0, Detail: strings.Join(details, "; ")}
		// A Probed carries one attribution; credit the service that actually
		// answered, preferring the POC (the in-force programa) when both did.
		if len(hits) > 0 || len(phits) == 0 {
			out.Source = src
		} else {
			out.Source = psrc
		}
		return out, nil
	}
}

// coastBandsProbe answers the POC safeguard bands: coastal erosion and
// overtopping/flooding safeguard strips and the terrestrial protection zone.
func coastBandsProbe(opts source.Options) source.Prober {
	bands := []struct {
		layer int
		label string
	}{
		{10, "faixa de salvaguarda ao galgamento e inundação costeira"},
		{11, "faixa de salvaguarda à erosão costeira"},
		{16, "zona terrestre de proteção"},
	}
	probes := make([]func(context.Context, source.BBox) ([]spatial.Feature, model.Source, error), len(bands))
	for i, b := range bands {
		probes[i] = source.ArcGISProbe(source.ArcGISProbeConfig{
			LayerURL: fmt.Sprintf("%s/POC/MapServer/%d", apa, b.layer),
			Meta:     model.Source{Name: "APA/SNIAmb — POC, faixas de salvaguarda e zonas de proteção", Layer: "poc-salvaguarda"},
		}, opts)
	}
	return func(ctx context.Context, subject source.BBox) (source.Probed, error) {
		var details []string
		var src model.Source
		for i, b := range bands {
			hits, s, err := probes[i](ctx, subject)
			if err != nil {
				return source.Probed{}, err
			}
			src = s
			if len(hits) == 0 {
				continue
			}
			d := b.label
			if n := hits[0].Prop("poc", "legenda_1"); n != "" {
				d += " (" + n + ")"
			}
			details = append(details, d)
		}
		return source.Probed{
			Present: len(details) > 0,
			Detail:  strings.Join(details, "; "),
			Source:  src,
		}, nil
	}
}

// poaapProbe answers the zonamento of the digitized reservoir plans (POAAP).
// Only 11 of the 41 POAAPs in force are digitized in this service; the bundled
// instruments registry names the plan applicable to each municipality either
// way, so an empty answer here is complementary, not contradictory.
func poaapProbe(opts source.Options) source.Prober {
	probe := source.ArcGISProbe(source.ArcGISProbeConfig{
		LayerURL: apa + "/POAAP/MapServer/0", OutFields: []string{"nome", "zonamento", "tipo", "local"},
		Meta: model.Source{Name: "APA/SNIAmb — POAAP (planos de albufeira digitalizados)", Layer: "poaap"},
	}, opts)
	return func(ctx context.Context, subject source.BBox) (source.Probed, error) {
		hits, src, err := probe(ctx, subject)
		if err != nil {
			return source.Probed{}, err
		}
		if len(hits) == 0 {
			return source.Probed{Source: src}, nil
		}
		var details []string
		seen := map[string]bool{}
		for _, h := range hits {
			d := h.Prop("nome")
			if z := h.Prop("zonamento", "tipo"); z != "" {
				d += ": " + z
			}
			if d != "" && !seen[d] {
				seen[d] = true
				details = append(details, d)
			}
		}
		return source.Probed{
			Present: true,
			Detail:  strings.Join(details, "; "),
			Note:    "Zonamento do POAAP digitalizado pela APA; consultar o regulamento do plano para o regime aplicável.",
			Source:  src,
		}, nil
	}
}

// paapProbe answers the área de intervenção of the new-generation programas
// especiais de albufeira (currently only Foz Tua is approved and loaded).
func paapProbe(opts source.Options) source.Prober {
	probe := source.ArcGISProbe(source.ArcGISProbeConfig{
		LayerURL: apa + "/PAAP_Sniamb/MapServer/1", OutFields: []string{"paap", "fase_situacao"},
		Meta: model.Source{Name: "APA/SNIAmb — Programas de Albufeira (PAAP)", Layer: "paap"},
	}, opts)
	return func(ctx context.Context, subject source.BBox) (source.Probed, error) {
		hits, src, err := probe(ctx, subject)
		if err != nil {
			return source.Probed{}, err
		}
		if len(hits) == 0 {
			return source.Probed{Source: src}, nil
		}
		d := "área de intervenção do PAAP " + hits[0].Prop("paap")
		if f := hits[0].Prop("fase_situacao"); f != "" {
			d += " (fase: " + f + ")"
		}
		return source.Probed{Present: true, Detail: d, Source: src}, nil
	}
}
