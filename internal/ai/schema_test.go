package ai

import (
	"encoding/json"
	"reflect"
	"testing"
)

// The structured-output API requires every object to declare
// additionalProperties:false and list all properties as required.
func TestAnalysisSchemaStrictness(t *testing.T) {
	var walk func(t *testing.T, node map[string]any, path string)
	walk = func(t *testing.T, node map[string]any, path string) {
		if node["type"] == "object" {
			if ap, ok := node["additionalProperties"].(bool); !ok || ap {
				t.Errorf("%s: object must set additionalProperties:false", path)
			}
			props, _ := node["properties"].(map[string]any)
			req, _ := node["required"].([]string)
			if len(req) != len(props) {
				t.Errorf("%s: required (%d) must cover all properties (%d)", path, len(req), len(props))
			}
			for name, sub := range props {
				if m, ok := sub.(map[string]any); ok {
					walk(t, m, path+"."+name)
				}
			}
		}
		if items, ok := node["items"].(map[string]any); ok {
			walk(t, items, path+"[]")
		}
	}
	walk(t, analysisSchema(), "root")
}

func TestAnalysisSchemaEnums(t *testing.T) {
	s := analysisSchema()
	sections := s["properties"].(map[string]any)["sections"].(map[string]any)
	idEnum := sections["items"].(map[string]any)["properties"].(map[string]any)["id"].(map[string]any)["enum"].([]string)
	if !reflect.DeepEqual(idEnum, SectionIDs) {
		t.Errorf("section id enum %v != SectionIDs %v", idEnum, SectionIDs)
	}
	if _, err := json.Marshal(s); err != nil {
		t.Fatalf("schema does not marshal: %v", err)
	}
}
