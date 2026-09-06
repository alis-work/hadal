package translation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIProviderRequestUsesStrictSchemaAndTranslationInstructions(t *testing.T) {
	var request openAIRequest
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, incoming *http.Request) {
		if incoming.URL.Path != "/chat/completions" || incoming.Method != http.MethodPost {
			t.Errorf("unexpected request: %s %s", incoming.Method, incoming.URL.Path)
		}
		if incoming.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("unexpected authorization header: %q", incoming.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(incoming.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		writeOpenAIResponse(t, writer, `{"source_language":"so","target_language":"en","translated_text":"Hello","interpreted_source":null,"clarification_required":false,"clarification_question":null}`)
	}))
	defer server.Close()

	provider := NewOpenAIProvider(server.Client(), server.URL, "secret", "gpt-test")
	result, err := provider.Translate(context.Background(), "Salaan")
	if err != nil || result.TranslatedText != "Hello" {
		t.Fatalf("unexpected result: %+v, %v", result, err)
	}
	if request.Model != "gpt-test" || len(request.Messages) != 2 || request.Messages[1].Content != "Salaan" {
		t.Fatalf("unexpected payload: %+v", request)
	}
	prompt := request.Messages[0].Content
	for _, phrase := range []string{"unsupported", "obvious spelling or grammar", "Preserve every fact", "tone", "do not guess", "clarification_question"} {
		if !strings.Contains(prompt, phrase) {
			t.Errorf("prompt omitted %q: %s", phrase, prompt)
		}
	}
	if request.ResponseFormat.Type != "json_schema" || !request.ResponseFormat.JSONSchema.Strict {
		t.Fatalf("response schema is not strict: %+v", request.ResponseFormat)
	}
	schema := request.ResponseFormat.JSONSchema.Schema
	if schema["additionalProperties"] != false {
		t.Fatalf("schema permits additional properties: %#v", schema)
	}
	required := schema["required"].([]any)
	if len(required) != 6 {
		t.Fatalf("all properties must be required by strict mode: %#v", required)
	}
}

func TestOpenAIProviderParsesBothLanguagePairs(t *testing.T) {
	tests := []struct {
		name    string
		content string
		source  string
		target  string
	}{
		{"Somali to English", `{"source_language":"so","target_language":"en","translated_text":"How are you?","interpreted_source":"Sidee tahay?","clarification_required":false,"clarification_question":null}`, Somali, English},
		{"English to Somali", `{"source_language":"en","target_language":"so","translated_text":"Mahadsanid","interpreted_source":null,"clarification_required":false,"clarification_question":null}`, English, Somali},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider, closeServer := testOpenAIProvider(t, http.StatusOK, test.content)
			defer closeServer()
			result, err := provider.Translate(context.Background(), "source")
			if err != nil || result.SourceLanguage != test.source || result.TargetLanguage != test.target {
				t.Fatalf("unexpected result: %+v, %v", result, err)
			}
		})
	}
}

func TestOpenAIProviderParsesClarificationWithoutInventingTranslation(t *testing.T) {
	provider, closeServer := testOpenAIProvider(t, http.StatusOK, `{"source_language":"so","target_language":"en","translated_text":null,"interpreted_source":null,"clarification_required":true,"clarification_question":"Maxaad uga jeeddaa bangiga?"}`)
	defer closeServer()
	result, err := provider.Translate(context.Background(), "bangiga")
	if err != nil || !result.ClarificationRequired || result.TranslatedText != "" || result.ClarificationQuestion == nil {
		t.Fatalf("unexpected clarification: %+v, %v", result, err)
	}
	if FormatReply(result) != "Maxaad uga jeeddaa bangiga?" {
		t.Fatalf("unexpected reply: %q", FormatReply(result))
	}
}

func TestOpenAIProviderRejectsUnsupportedLanguageWithoutTranslation(t *testing.T) {
	provider, closeServer := testOpenAIProvider(t, http.StatusOK, `{"source_language":"unsupported","target_language":null,"translated_text":null,"interpreted_source":null,"clarification_required":false,"clarification_question":null}`)
	defer closeServer()
	result, err := provider.Translate(context.Background(), "Bonjour")
	if err != nil || result.SourceLanguage != Unsupported || result.TargetLanguage != "" || result.TranslatedText != "" {
		t.Fatalf("unexpected unsupported result: %+v, %v", result, err)
	}
	if FormatReply(result) != UnsupportedLanguageReply {
		t.Fatalf("unexpected reply: %q", FormatReply(result))
	}
}

func TestOpenAIProviderRejectsInvalidStructuredOutputPermanently(t *testing.T) {
	tests := []string{
		`{"source_language":"so","target_language":"so","translated_text":"x","interpreted_source":null,"clarification_required":false,"clarification_question":null}`,
		`{"source_language":"so","target_language":"en","translated_text":null,"interpreted_source":null,"clarification_required":false,"clarification_question":null}`,
		`{"source_language":"so","target_language":"en","translated_text":"guess","interpreted_source":null,"clarification_required":true,"clarification_question":"Which?"}`,
		`{"source_language":"so","target_language":"en","translated_text":"x","interpreted_source":null,"clarification_required":false,"clarification_question":null,"extra":true}`,
		`{"source_language":"so","target_language":"en","translated_text":"x","clarification_required":false,"clarification_question":null}`,
		`{"source_language":"unsupported","target_language":"en","translated_text":null,"interpreted_source":null,"clarification_required":false,"clarification_question":null}`,
		`{"source_language":"unsupported","target_language":null,"translated_text":"Bonjour","interpreted_source":null,"clarification_required":false,"clarification_question":null}`,
	}
	for _, content := range tests {
		provider, closeServer := testOpenAIProvider(t, http.StatusOK, content)
		_, err := provider.Translate(context.Background(), "source")
		closeServer()
		if !errors.Is(err, ErrInvalidResult) || IsTransient(err) {
			t.Fatalf("expected permanent invalid result for %s, got %v", content, err)
		}
	}
}

func TestOpenAIProviderClassifiesFailures(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway} {
		provider, closeServer := testOpenAIProvider(t, status, "")
		_, err := provider.Translate(context.Background(), "source")
		closeServer()
		if !IsTransient(err) {
			t.Errorf("HTTP %d should be transient: %v", status, err)
		}
	}
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden} {
		provider, closeServer := testOpenAIProvider(t, status, "")
		_, err := provider.Translate(context.Background(), "source")
		closeServer()
		if err == nil || IsTransient(err) {
			t.Errorf("HTTP %d should be permanent: %v", status, err)
		}
	}
	provider := NewOpenAIProvider(roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, io.ErrUnexpectedEOF
	}), "https://example.test", "secret", "model")
	_, err := provider.Translate(context.Background(), "source")
	if !IsTransient(err) {
		t.Fatalf("network error should be transient: %v", err)
	}
}

func TestOpenAIProviderRejectsInvalidSourceBeforeHTTP(t *testing.T) {
	called := false
	provider := NewOpenAIProvider(roundTripperFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("unexpected")
	}), "https://example.test", "secret", "model")
	if _, err := provider.Translate(context.Background(), " \n "); !errors.Is(err, ErrEmptySource) {
		t.Fatalf("expected empty source error, got %v", err)
	}
	if _, err := provider.Translate(context.Background(), strings.Repeat("é", MaxSourceBytes/2+1)); !errors.Is(err, ErrSourceTooLong) {
		t.Fatalf("expected byte limit error, got %v", err)
	}
	if called {
		t.Fatal("invalid source called provider")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (function roundTripperFunc) Do(request *http.Request) (*http.Response, error) {
	return function(request)
}

func testOpenAIProvider(t *testing.T, status int, content string) (*OpenAIProvider, func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(status)
		if status == http.StatusOK {
			writeOpenAIResponse(t, writer, content)
		}
	}))
	return NewOpenAIProvider(server.Client(), server.URL, "secret", "model"), server.Close
}

func writeOpenAIResponse(t *testing.T, writer http.ResponseWriter, content string) {
	t.Helper()
	if err := json.NewEncoder(writer).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}}); err != nil {
		t.Error(err)
	}
}
