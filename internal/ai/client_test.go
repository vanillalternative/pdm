package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"
)

// rtFunc adapts a function to http.RoundTripper for stubbing the API.
type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func stubClient(t *testing.T, status int, body string, sawBody *string) *Client {
	t.Helper()
	httpc := &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		if sawBody != nil {
			data, _ := io.ReadAll(r.Body)
			*sawBody = string(data)
		}
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}, nil
	})}
	c, err := New(TierBasic,
		option.WithAPIKey("test-key"),
		option.WithHTTPClient(httpc),
		option.WithMaxRetries(0),
	)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// apiResponse wraps an analysis JSON as a canned /v1/messages response.
func apiResponse(t *testing.T, analysisJSON, stopReason string) string {
	t.Helper()
	text, err := json.Marshal(analysisJSON)
	if err != nil {
		t.Fatal(err)
	}
	return `{"id":"msg_test","type":"message","role":"assistant","model":"claude-haiku-4-5-20251001",
		"content":[{"type":"text","text":` + string(text) + `}],
		"stop_reason":"` + stopReason + `","usage":{"input_tokens":10,"output_tokens":10}}`
}

const validAnalysis = `{
	"title":"Análise urbanística — Tomar",
	"executive_summary":"Parcela em solo rústico com REN presente.",
	"viability_signal":"condicionado",
	"sections":[
		{"id":"leitura_estrategica","heading":"Leitura estratégica","reading":"Abordagem cautelosa.","notes":""},
		{"id":"identificacao","heading":"Identificação","reading":"Localiza-se em Tomar.","notes":"Nota legal."}
	],
	"citations":[
		{"article_number":"31.º","relevance":"Regime dos espaços agrícolas"},
		{"article_number":"99.º","relevance":"Inventado pelo modelo"}
	],
	"uncertainties":["Sem dados de topografia."]
}`

func testPayload(t *testing.T) Payload {
	t.Helper()
	p, err := BuildPointPayload(fixtureResult())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGenerate(t *testing.T) {
	var sawBody string
	c := stubClient(t, 200, apiResponse(t, validAnalysis, "end_turn"), &sawBody)
	a, err := c.Generate(context.Background(), testPayload(t))
	if err != nil {
		t.Fatal(err)
	}
	// Request carries the schema, the grounding prompt, and the payload.
	for _, want := range []string{"output_config", "json_schema", "viability_signal", "NEVER invent", "Tomar"} {
		if !strings.Contains(sawBody, want) {
			t.Errorf("request body missing %q", want)
		}
	}
	// Sections are reordered canonically (identificacao before leitura_estrategica).
	if len(a.Sections) != 2 || a.Sections[0].ID != "identificacao" || a.Sections[1].ID != "leitura_estrategica" {
		t.Errorf("sections not canonically ordered: %+v", a.Sections)
	}
	// The invented citation is dropped and disclosed.
	if len(a.Citations) != 1 || a.Citations[0].ArticleNumber != "31.º" {
		t.Errorf("citations not validated: %+v", a.Citations)
	}
	joined := strings.Join(a.Uncertainties, "\n")
	if !strings.Contains(joined, "99.º") {
		t.Errorf("dropped citation not disclosed in uncertainties: %v", a.Uncertainties)
	}
	if a.ViabilitySignal != ViabilityCondicionado {
		t.Errorf("viability = %q", a.ViabilitySignal)
	}
}

func TestGenerateErrors(t *testing.T) {
	apiErr := func(status int, typ string) string {
		return `{"type":"error","error":{"type":"` + typ + `","message":"boom"}}`
	}
	cases := []struct {
		name    string
		status  int
		body    string
		wantErr string
	}{
		{"rate limited", 429, apiErr(429, "rate_limit_error"), "rate limited"},
		{"bad key", 401, apiErr(401, "authentication_error"), "ANTHROPIC_API_KEY"},
		{"overloaded", 529, apiErr(529, "overloaded_error"), "overloaded"},
		{"truncated", 200, apiResponse(t, validAnalysis, "max_tokens"), "truncated"},
		{"invalid json", 200, apiResponse(t, "not json{", "end_turn"), "invalid analysis JSON"},
		{"unknown section", 200, apiResponse(t, `{"title":"t","executive_summary":"s","viability_signal":"favoravel",
			"sections":[{"id":"made_up","heading":"h","reading":"r","notes":""}],"citations":[],"uncertainties":[]}`, "end_turn"),
			"unknown analysis sections"},
		{"no sections", 200, apiResponse(t, `{"title":"t","executive_summary":"s","viability_signal":"favoravel",
			"sections":[],"citations":[],"uncertainties":[]}`, "end_turn"),
			"no analysis sections"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := stubClient(t, tc.status, tc.body, nil)
			_, err := c.Generate(context.Background(), testPayload(t))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestNewRequiresKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	if _, err := New(TierBasic); err == nil || !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Fatalf("expected actionable missing-key error, got %v", err)
	}
}

func TestParseTier(t *testing.T) {
	if tier, err := ParseTier("premium"); err != nil || tier != TierPremium {
		t.Errorf("premium: %v %v", tier, err)
	}
	if _, err := ParseTier("bogus"); err == nil {
		t.Error("bogus tier should error")
	}
}
