package ai

// analysisSchema is the JSON schema enforced through the API's structured
// output. Constraints the schema cannot express (section completeness,
// citation grounding) are validated in Go after unmarshalling.
func analysisSchema() map[string]any {
	str := map[string]any{"type": "string"}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required": []string{
			"title", "executive_summary", "viability_signal",
			"sections", "citations", "uncertainties",
		},
		"properties": map[string]any{
			"title":             str,
			"executive_summary": str,
			"viability_signal": map[string]any{
				"type": "string",
				"enum": []string{
					ViabilityFavoravel, ViabilityCondicionado,
					ViabilityDesfavoravel, ViabilityIndeterminado,
				},
			},
			"sections": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"id", "heading", "reading", "notes"},
					"properties": map[string]any{
						"id":      map[string]any{"type": "string", "enum": SectionIDs},
						"heading": str,
						"reading": str,
						"notes":   str,
					},
				},
			},
			"citations": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"article_number", "relevance"},
					"properties": map[string]any{
						"article_number": str,
						"relevance":      str,
					},
				},
			},
			"uncertainties": map[string]any{"type": "array", "items": str},
		},
	}
}
