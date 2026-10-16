package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/Qsnh/Murmur/locales"
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
	thinkBetweenTools
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
	case family == "sonnet" && (major > 5 || major == 5 && minor >= 5):
		return thinkBetweenTools
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
		if text := m.text(); strings.TrimSpace(text) != "" || len(blocks) == 0 {
			blocks = append(blocks, anthropic.NewTextBlock(text))
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
		budget := thinkingBudget(req.Effort, maxTokens)
		if budget >= 1024 {
			params.Thinking = anthropic.ThinkingConfigParamOfEnabled(budget)
		}
	case req.Thinking:
		// Summarized, so that the reasoning shows as it comes rather than
		// as a long pause.
		adaptive := anthropic.ThinkingConfigAdaptiveParam{Display: anthropic.ThinkingConfigAdaptiveDisplaySummarized}
		params.Thinking = anthropic.ThinkingConfigParamUnion{OfAdaptive: &adaptive}
		params.OutputConfig = anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffort(req.Effort)}
	case kind == thinkAdaptive:
		params.Thinking = anthropic.ThinkingConfigParamUnion{OfDisabled: &anthropic.ThinkingConfigDisabledParam{}}
	case kind == thinkBetweenTools:
		off := anthropic.NewThinkingConfigBetweenToolsParam()
		params.Thinking = anthropic.ThinkingConfigParamUnion{OfBetweenTools: &off}
	case kind == thinkAlways:
		params.OutputConfig = anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffortLow}
	}

	if len(req.Tools) > 0 {
		params.Tools = anthropicTools(req.Tools)
	}
	out := &rounds{emit: emit}
	for round := 0; ; round++ {
		if len(req.Tools) > 0 && round == maxToolRounds-1 {
			none := anthropic.NewToolChoiceNoneParam()
			params.ToolChoice = anthropic.ToolChoiceUnionParam{OfNone: &none}
		}
		msg, err := anthropicRound(ctx, c, params, out.send)
		if err != nil {
			var se *statusError
			if len(req.Tools) > 0 && errors.As(err, &se) && toolsRejected(round, se.status, se.msg) {
				return toolsError{err}
			}
			return err
		}
		// What was read from the cache, or written to it, was read too.
		u := msg.Usage
		out.add(&Usage{Input: int(u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens), Output: int(u.OutputTokens)})
		if msg.StopReason == anthropic.StopReasonRefusal {
			return errors.New(locales.LlmRefused())
		}
		var calls []anthropic.ToolUseBlock
		for _, b := range msg.Content {
			if b.Type == "tool_use" {
				calls = append(calls, b.AsToolUse())
			}
		}
		if msg.StopReason != anthropic.StopReasonToolUse || len(calls) == 0 || round == maxToolRounds-1 {
			break
		}
		params.Messages = append(params.Messages, assistantParam(msg))
		var results []anthropic.ContentBlockParamUnion
		for _, call := range calls {
			result, failed := runTool(ctx, req.Tools, call.Name, call.Input)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			results = append(results, anthropic.NewToolResultBlock(call.ID, result, failed))
		}
		params.Messages = append(params.Messages, anthropic.NewUserMessage(results...))
		out.next()
	}
	out.done()
	return nil
}

func anthropicTools(tools []Tool) []anthropic.ToolUnionParam {
	var out []anthropic.ToolUnionParam
	for _, t := range tools {
		tool := anthropic.ToolParam{
			Name:        t.Name,
			Description: anthropic.String(t.Description),
			InputSchema: anthropic.ToolInputSchemaParam{Properties: t.Properties, Required: t.Required},
		}
		out = append(out, anthropic.ToolUnionParam{OfTool: &tool})
	}
	return out
}

func assistantParam(msg anthropic.Message) anthropic.MessageParam {
	p := msg.ToParam()
	p.Role = anthropic.MessageParamRoleAssistant
	var blocks []anthropic.ContentBlockParamUnion
	for _, b := range p.Content {
		if b.OfText != nil && strings.TrimSpace(b.OfText.Text) == "" {
			continue
		}
		blocks = append(blocks, b)
	}
	p.Content = blocks
	return p
}

func anthropicRound(ctx context.Context, c anthropic.Client, params anthropic.MessageNewParams, emit func(Event)) (anthropic.Message, error) {
	stream := c.Messages.NewStreaming(ctx, params)
	defer stream.Close()
	var msg anthropic.Message
	var accErr error
	for stream.Next() {
		ev := stream.Current()
		if err := msg.Accumulate(ev); err != nil && accErr == nil {
			accErr = err
		}
		switch ev := ev.AsAny().(type) {
		case anthropic.ContentBlockDeltaEvent:
			switch d := ev.Delta.AsAny().(type) {
			case anthropic.TextDelta:
				emit(Event{Text: d.Text})
			case anthropic.ThinkingDelta:
				emit(Event{Reasoning: d.Thinking})
			}
		}
	}
	if err := stream.Err(); err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) {
			m := apiErrorMessage(apiErr)
			return msg, &statusError{status: apiErr.StatusCode, msg: m, text: locales.LlmHTTPError(strconv.Itoa(apiErr.StatusCode), m)}
		}
		return msg, err
	}
	if accErr != nil && msg.StopReason == anthropic.StopReasonToolUse {
		return msg, accErr
	}
	return msg, nil
}

func thinkingBudget(effort Effort, maxTokens int64) int64 {
	switch effort {
	case EffortLow:
		return min(maxTokens/4, 4000)
	case EffortHigh:
		return min(maxTokens*3/4, 32000)
	}
	return min(maxTokens/2, 16000)
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
