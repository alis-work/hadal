package translation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const openAISystemPrompt = `You are Hadal's Somali-English translator. Detect whether the user's source text is Somali (so), English (en), or unsupported. If it is not predominantly Somali or English, set source_language to unsupported and every nullable field to null; do not translate it or ask a clarification question. For Somali or English, target the opposite language and translate the intended meaning naturally, correcting only obvious spelling or grammar issues needed to understand it. Preserve every fact, name, number, qualification, uncertainty, emphasis, and the speaker's tone; do not add explanations or information. interpreted_source may contain your minimally corrected understanding of the source when correction was needed, otherwise null. If supported input is too ambiguous to translate safely, do not guess: set translated_text to null, clarification_required to true, and ask one concise clarification_question in the source language. Otherwise set clarification_required to false and clarification_question to null. Return only JSON matching the schema.`

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type OpenAIProvider struct {
	client  HTTPClient
	baseURL string
	apiKey  string
	model   string
}

func NewOpenAIProvider(client HTTPClient, baseURL, apiKey, model string) *OpenAIProvider {
	if client == nil {
		client = http.DefaultClient
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &OpenAIProvider{client: client, baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, model: model}
}

type openAIRequest struct {
	Model          string          `json:"model"`
	Messages       []openAIMessage `json:"messages"`
	ResponseFormat responseFormat  `json:"response_format"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type       string     `json:"type"`
	JSONSchema jsonSchema `json:"json_schema"`
}

type jsonSchema struct {
	Name   string         `json:"name"`
	Strict bool           `json:"strict"`
	Schema map[string]any `json:"schema"`
}

func translationSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"source_language":        map[string]any{"type": "string", "enum": []string{Somali, English, Unsupported}},
			"target_language":        map[string]any{"type": []string{"string", "null"}, "enum": []any{Somali, English, nil}},
			"translated_text":        map[string]any{"type": []string{"string", "null"}},
			"interpreted_source":     map[string]any{"type": []string{"string", "null"}},
			"clarification_required": map[string]any{"type": "boolean"},
			"clarification_question": map[string]any{"type": []string{"string", "null"}},
		},
		"required":             []string{"source_language", "target_language", "translated_text", "interpreted_source", "clarification_required", "clarification_question"},
		"additionalProperties": false,
	}
}

func (p *OpenAIProvider) Translate(ctx context.Context, source string) (Result, error) {
	if err := validateSource(source); err != nil {
		return Result{}, err
	}
	payload := openAIRequest{
		Model:          p.model,
		Messages:       []openAIMessage{{Role: "system", Content: openAISystemPrompt}, {Role: "user", Content: source}},
		ResponseFormat: responseFormat{Type: "json_schema", JSONSchema: jsonSchema{Name: "hadal_translation", Strict: true, Schema: translationSchema()}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Result{}, fmt.Errorf("encode OpenAI request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Result{}, fmt.Errorf("create OpenAI request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+p.apiKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(request)
	if err != nil {
		return Result{}, transient(fmt.Errorf("OpenAI translation request: %w", err))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		err := fmt.Errorf("OpenAI translation returned HTTP %d", response.StatusCode)
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
			return Result{}, transient(err)
		}
		return Result{}, err
	}

	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
				Refusal string `json:"refusal"`
			} `json:"message"`
		} `json:"choices"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&envelope); err != nil {
		return Result{}, fmt.Errorf("%w: decode OpenAI response: %v", ErrInvalidResult, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Result{}, err
	}
	if len(envelope.Choices) != 1 || envelope.Choices[0].Message.Refusal != "" || strings.TrimSpace(envelope.Choices[0].Message.Content) == "" {
		return Result{}, fmt.Errorf("%w: OpenAI response omitted one usable choice", ErrInvalidResult)
	}
	result, err := decodeResult(envelope.Choices[0].Message.Content)
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

type wireResult struct {
	SourceLanguage        string  `json:"source_language"`
	TargetLanguage        *string `json:"target_language"`
	TranslatedText        *string `json:"translated_text"`
	InterpretedSource     *string `json:"interpreted_source"`
	ClarificationRequired bool    `json:"clarification_required"`
	ClarificationQuestion *string `json:"clarification_question"`
}

func decodeResult(content string) (Result, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &fields); err != nil {
		return Result{}, fmt.Errorf("%w: decode structured output: %v", ErrInvalidResult, err)
	}
	required := []string{"source_language", "target_language", "translated_text", "interpreted_source", "clarification_required", "clarification_question"}
	if len(fields) != len(required) {
		return Result{}, fmt.Errorf("%w: structured output has incorrect fields", ErrInvalidResult)
	}
	for _, name := range required {
		if _, ok := fields[name]; !ok {
			return Result{}, fmt.Errorf("%w: structured output omitted %s", ErrInvalidResult, name)
		}
	}
	var wire wireResult
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return Result{}, fmt.Errorf("%w: decode structured output: %v", ErrInvalidResult, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Result{}, err
	}
	result := Result{SourceLanguage: wire.SourceLanguage, InterpretedSource: trimOptional(wire.InterpretedSource), ClarificationRequired: wire.ClarificationRequired, ClarificationQuestion: trimOptional(wire.ClarificationQuestion)}
	if target := trimOptional(wire.TargetLanguage); target != nil {
		result.TargetLanguage = *target
	}
	if wire.TranslatedText != nil {
		result.TranslatedText = strings.TrimSpace(*wire.TranslatedText)
	}
	if err := result.Validate(); err != nil {
		return Result{}, err
	}
	return result, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: structured output contains trailing data", ErrInvalidResult)
	}
	return nil
}

func trimOptional(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	return &trimmed
}

func validateSource(source string) error {
	if strings.TrimSpace(source) == "" {
		return ErrEmptySource
	}
	if len(source) > MaxSourceBytes {
		return ErrSourceTooLong
	}
	return nil
}
