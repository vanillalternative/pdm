package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// tierModels maps a pricing tier to the Claude model behind it.
var tierModels = map[Tier]anthropic.Model{
	TierBasic:   anthropic.ModelClaudeHaiku4_5_20251001,
	TierPremium: anthropic.ModelClaudeOpus4_8,
}

// ModelFor returns the model id used by a tier (for display/labelling).
func ModelFor(tier Tier) string {
	return string(tierModels[tier])
}

// Client generates analyses through the Anthropic API.
type Client struct {
	api      anthropic.Client
	model    anthropic.Model
	thinking bool
}

// New builds a client for the tier. Credentials come from ANTHROPIC_API_KEY;
// extra options exist for tests (option.WithHTTPClient, option.WithAPIKey).
func New(tier Tier, opts ...option.RequestOption) (*Client, error) {
	m, ok := tierModels[tier]
	if !ok {
		return nil, fmt.Errorf("unknown tier %q", tier)
	}
	if os.Getenv("ANTHROPIC_API_KEY") == "" && len(opts) == 0 {
		return nil, errors.New("analysis requires an Anthropic API key: set ANTHROPIC_API_KEY (create one at https://console.anthropic.com/settings/keys)")
	}
	return &Client{
		api:      anthropic.NewClient(opts...),
		model:    m,
		thinking: tier == TierPremium,
	}, nil
}

// Generate sends the payload to the model and returns the validated analysis.
func (c *Client) Generate(ctx context.Context, p Payload) (*Analysis, error) {
	user, err := userMessage(p)
	if err != nil {
		return nil, err
	}
	params := anthropic.MessageNewParams{
		Model:     c.model,
		MaxTokens: 8192,
		System:    []anthropic.TextBlockParam{{Text: systemPrompt}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(user)),
		},
		OutputConfig: anthropic.OutputConfigParam{
			Format: anthropic.JSONOutputFormatParam{Schema: analysisSchema()},
		},
	}
	if c.thinking {
		params.Thinking = anthropic.ThinkingConfigParamUnion{
			OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{},
		}
	}
	resp, err := c.api.Messages.New(ctx, params)
	if err != nil {
		return nil, friendlyError(err)
	}
	switch resp.StopReason {
	case anthropic.StopReasonMaxTokens:
		return nil, errors.New("analysis was truncated by the output limit — please retry")
	case anthropic.StopReasonRefusal:
		return nil, errors.New("the model declined to produce this analysis — please retry or report the query")
	}
	var text strings.Builder
	for _, block := range resp.Content {
		if tb, ok := block.AsAny().(anthropic.TextBlock); ok {
			text.WriteString(tb.Text)
		}
	}
	var a Analysis
	if err := json.Unmarshal([]byte(text.String()), &a); err != nil {
		return nil, fmt.Errorf("model returned invalid analysis JSON: %w", err)
	}
	if err := validate(&a, p.ArticleNumbers); err != nil {
		return nil, err
	}
	return &a, nil
}

// validate enforces what the JSON schema cannot: canonical section order
// without duplicates, and citations grounded in the payload's articles.
// Ungrounded citations are dropped and disclosed, never rendered.
func validate(a *Analysis, articleNumbers []string) error {
	known := make(map[string]bool, len(articleNumbers))
	for _, n := range articleNumbers {
		known[n] = true
	}
	kept := a.Citations[:0]
	for _, c := range a.Citations {
		if known[c.ArticleNumber] {
			kept = append(kept, c)
			continue
		}
		a.Uncertainties = append(a.Uncertainties,
			fmt.Sprintf("Citação removida na validação: o artigo %q não consta dos dados desta análise.", c.ArticleNumber))
	}
	a.Citations = kept

	bySection := make(map[string]Section, len(a.Sections))
	for _, s := range a.Sections {
		if _, dup := bySection[s.ID]; dup {
			return fmt.Errorf("model returned duplicate section %q", s.ID)
		}
		bySection[s.ID] = s
	}
	ordered := make([]Section, 0, len(SectionIDs))
	for _, id := range SectionIDs {
		if s, ok := bySection[id]; ok {
			ordered = append(ordered, s)
			delete(bySection, id)
		}
	}
	if len(bySection) > 0 {
		return fmt.Errorf("model returned unknown analysis sections")
	}
	if len(ordered) == 0 {
		return errors.New("model returned no analysis sections")
	}
	a.Sections = ordered

	switch a.ViabilitySignal {
	case ViabilityFavoravel, ViabilityCondicionado, ViabilityDesfavoravel, ViabilityIndeterminado:
	default:
		a.ViabilitySignal = ViabilityIndeterminado
	}
	return nil
}

// friendlyError maps API failures to actionable CLI messages.
func friendlyError(err error) error {
	var apierr *anthropic.Error
	if errors.As(err, &apierr) {
		switch apierr.StatusCode {
		case 401:
			return errors.New("the Anthropic API rejected the key: check ANTHROPIC_API_KEY")
		case 429:
			return errors.New("rate limited by the Anthropic API (already retried) — wait a moment and retry")
		case 500, 529:
			return errors.New("the Anthropic API is temporarily overloaded — retry shortly")
		}
		return fmt.Errorf("Anthropic API error (HTTP %d): %w", apierr.StatusCode, err)
	}
	return fmt.Errorf("could not reach the Anthropic API: %w", err)
}
