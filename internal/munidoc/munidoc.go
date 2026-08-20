// Package munidoc assembles the municipalities JSON document emitted by
// `pdm municipalities` and served by `pdm serve`. Its shape is a contract
// consumed by server.js — keep the field names stable.
package munidoc

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/bernardosimoes/pdm/data"
	"github.com/bernardosimoes/pdm/internal/admin"
	"github.com/bernardosimoes/pdm/internal/planos"
)

// Doc is the top-level document.
type Doc struct {
	Count          int     `json:"count"`
	Municipalities []Entry `json:"municipalities"`
}

type Entry struct {
	Name         string        `json:"name"`
	Code         string        `json:"code"`
	District     string        `json:"district"`
	Centroid     Centroid      `json:"centroid"`
	BBox         [4]float64    `json:"bbox"` // minLon, minLat, maxLon, maxLat
	Regulamento  *Regulamento  `json:"regulamento"`
	SpecialPlans []SpecialPlan `json:"special_plans"`
}

type Centroid struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type Regulamento struct {
	Reference string `json:"reference"`
	URL       string `json:"url"`
	Articles  int    `json:"articles"`
	Status    string `json:"status"`
}

type SpecialPlan struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	State   string `json:"state"`
	Diploma string `json:"diploma"`
}

// Build joins three bundled sources into the document: administrative
// boundaries (name/code/district/centroid/bbox), the parsed-regulamento
// coverage manifest (keyed by dtcc), and the special-plans registry (by name).
func Build(resolver *admin.Resolver) Doc {
	regByCode := loadRegulamentoIndex()

	list := resolver.List()
	doc := Doc{Count: len(list), Municipalities: make([]Entry, 0, len(list))}
	for _, m := range list {
		entry := Entry{
			Name:         m.Name,
			Code:         m.Code,
			District:     m.District,
			Centroid:     Centroid{Lat: m.CentroidLat, Lon: m.CentroidLon},
			BBox:         m.BBox,
			SpecialPlans: []SpecialPlan{},
		}
		if r, ok := regByCode[m.Code]; ok {
			r := r
			entry.Regulamento = &r
		}
		for _, ins := range planos.ForMunicipality(m.Name) {
			entry.SpecialPlans = append(entry.SpecialPlans, SpecialPlan{
				Name:    ins.Name,
				Kind:    ins.Kind,
				State:   ins.State,
				Diploma: ins.Diploma,
			})
		}
		doc.Municipalities = append(doc.Municipalities, entry)
	}
	return doc
}

// Encode writes the document as indented JSON with HTML escaping off, the
// exact encoding `pdm municipalities` has always produced.
func Encode(w io.Writer, d Doc) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(d); err != nil {
		return fmt.Errorf("encoding municipalities: %w", err)
	}
	return nil
}

// loadRegulamentoIndex reads the bundled regulamentos coverage manifest and
// returns it keyed by dtcc. A missing or unparsable manifest yields an empty
// map (every municipality then reports a null regulamento).
func loadRegulamentoIndex() map[string]Regulamento {
	out := map[string]Regulamento{}
	b, err := data.Regulamentos.ReadFile("regulamentos/index.json")
	if err != nil {
		return out
	}
	var idx struct {
		Municipalities []struct {
			DTCC         string `json:"dtcc"`
			Reference    string `json:"reference"`
			URL          string `json:"url"`
			ArticleCount int    `json:"article_count"`
			Status       string `json:"status"`
		} `json:"municipalities"`
	}
	if err := json.Unmarshal(b, &idx); err != nil {
		return out
	}
	for _, m := range idx.Municipalities {
		out[m.DTCC] = Regulamento{
			Reference: m.Reference,
			URL:       m.URL,
			Articles:  m.ArticleCount,
			Status:    m.Status,
		}
	}
	return out
}
