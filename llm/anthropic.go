package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// claudeThinking is how a model of the Messages API thinks.
type claudeThinking int

const (
	// thinkBudget takes {type: enabled, budget_tokens}: Claude before 4.6,
	// and the models of other providers that speak the protocol.
	thinkBudget claudeThinking = iota
	// thinkAdaptive takes {type: adaptive}, and {type: disabled} to stop.
	thinkAdaptive
	// thinkAlways takes {type: adaptive} and cannot stop thinking: lower
	// effort is the way to think less.
	thinkAlways
)

var claudeModel = regexp.MustCompile(`claude-(opus|sonnet|haiku|fable|mythos)-(\d+)(?:-(\d{1,2}))?(?:[^0-9]|$)`)

// claudeThinkingOf returns how the model of the given ID thinks.
func claudeThinkingOf(model string) claudeThinking {
	m := claudeModel.FindStringSubmatch(strings.ToLower(model))
	if m == nil {
		return thinkBudget
	}
	family := m[1]
	major, _ := strconv.Atoi(m[2])
	minor, _ := strconv.Atoi(m[3])
	switch {
	case family == "fable" || family == "mythos":
		return thinkAlways
	case family == "opus" && (major > 5 || major == 5 && minor >= 5):
		return thinkAlways
	case major > 4 || major == 4 && minor >= 6:
		return thinkAdaptive
	}
	return thinkBudget
}

// anthropicBase returns the base URL the SDK adds /v1/messages to.
func anthropicBase(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	base = strings.TrimSuffix(base, "/v1/messages")
	base = strings.TrimSuffix(base, "/v1")
	if base == "" {
		return "https://api.anthropic.com"
	}
	return base
}

func streamAnthropic(ctx context.Context, client *http.Client, cfg Config, req Request, emit func(Event)) error {
	c := anthropic.NewClient(
		option.WithAPIKey(strings.TrimSpace(cfg.APIKey)),
		option.WithBaseURL(anthropicBase(cfg.BaseURL)),
		option.WithHTTPClient(client),
	)
	kind := claudeThinkingOf(cfg.Model)
	maxTokens := int64(cfg.MaxTokens)
	if maxTokens <= 0 {
		maxTokens = 8192
		if kind != thinkBudget || strings.Contains(strings.ToLower(cfg.Model), "claude-") {
			maxTokens = 32000
		}
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(strings.TrimSpace(cfg.Model)),
		MaxTokens: maxTokens,
	}
	if s := strings.TrimSpace(req.System); s != "" {
		params.System = []anthropic.TextBlockParam{{Text: s}}
	}
	for _, m := range req.Messages {
		// The pictures go before the text, as Anthropic advises; a text
		// block may not be blank.
		var blocks []anthropic.ContentBlockParamUnion
		for _, img := range m.Images {
			blocks = append(blocks, anthropic.NewImageBlockBase64(img.MIME, base64.StdEncoding.EncodeToString(img.Data)))
		}
		if strings.TrimSpace(m.Content) != "" || len(blocks) == 0 {
			blocks = append(blocks, anthropic.NewTextBlock(m.Content))
		}
		if m.Role == "assistant" {
			params.Messages = append(params.Messages, anthropic.NewAssistantMessage(blocks...))
		} else {
			params.Messages = append(params.Messages, anthropic.NewUserMessage(blocks...))
		}
	}

	switch {
	case req.Thinking && kind == thinkBudget:
		// The budget must be under max_tokens, and at least 1024.
		budget := min(maxTokens/2, 16000)
		if budget >= 1024 {
			params.Thinking = anthropic.ThinkingConfigParamOfEnabled(budget)
		}
	case req.Thinking:
		// Summarized, so that the reasoning shows as it comes rather than
		// as a long pause.
		adaptive := anthropic.ThinkingConfigAdaptiveParam{Display: anthropic.ThinkingConfigAdaptiveDisplaySummarized}
		params.Thinking = anthropic.ThinkingConfigParamUnion{OfAdaptive: &adaptive}
	case kind == thinkAdaptive:
		params.Thinking = anthropic.ThinkingConfigParamUnion{OfDisabled: &anthropic.ThinkingConfigDisabledParam{}}
	case kind == thinkAlways:
		params.OutputConfig = anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffortLow}
	}

	stream := c.Messages.NewStreaming(ctx, params)
	defer stream.Close()
	refused := false
	var usage anthropic.Usage
	for stream.Next() {
		switch ev := stream.Current().AsAny().(type) {
		case anthropic.MessageStartEvent:
			usage = ev.Message.Usage
		case anthropic.ContentBlockDeltaEvent:
			switch d := ev.Delta.AsAny().(type) {
			case anthropic.TextDelta:
				emit(Event{Text: d.Text})
			case anthropic.ThinkingDelta:
				emit(Event{Reasoning: d.Thinking})
			}
		case anthropic.MessageDeltaEvent:
			if ev.Delta.StopReason == anthropic.StopReasonRefusal {
				refused = true
			}
			// The counts here are totals so far, those left out unchanged
			// since message_start.
			u := ev.Usage
			usage.OutputTokens = u.OutputTokens
			if u.JSON.InputTokens.Valid() {
				usage.InputTokens = u.InputTokens
			}
			if u.JSON.CacheCreationInputTokens.Valid() {
				usage.CacheCreationInputTokens = u.CacheCreationInputTokens
			}
			if u.JSON.CacheReadInputTokens.Valid() {
				usage.CacheReadInputTokens = u.CacheReadInputTokens
			}
		}
	}
	if err := stream.Err(); err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) {
			return fmt.Errorf("HTTP %d：%s", apiErr.StatusCode, apiErrorMessage(apiErr))
		}
		return err
	}
	if refused {
		return fmt.Errorf("模型拒绝回答这个请求")
	}
	// What was read from the cache, or written to it, was read too.
	in := usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens
	if in > 0 || usage.OutputTokens > 0 {
		emit(Event{Usage: &Usage{Input: int(in), Output: int(usage.OutputTokens)}})
	}
	return nil
}

// apiErrorMessage returns the message of an error the API answered with.
func apiErrorMessage(e *anthropic.Error) string {
	raw := e.RawJSON()
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if raw != "" && json.Unmarshal([]byte(raw), &body) == nil && body.Error.Message != "" {
		return body.Error.Message
	}
	return http.StatusText(e.StatusCode)
}
