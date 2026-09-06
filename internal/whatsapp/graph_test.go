package whatsapp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGraphClientSendsTextWithBearerToken(t *testing.T) {
	var path, authorization, body string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		path = request.URL.Path
		authorization = request.Header.Get("Authorization")
		data, _ := io.ReadAll(request.Body)
		body = string(data)
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write([]byte(`{"messages":[{"id":"wamid.outbound"}]}`))
	}))
	defer server.Close()
	client := NewGraphClient(server.Client(), server.URL, "v22.0", "access-token")

	providerID, err := client.SendText(context.Background(), "phone-id", "+252611234567", "wamid.inbound", "hello")

	if err != nil {
		t.Fatal(err)
	}
	if path != "/v22.0/phone-id/messages" || authorization != "Bearer access-token" {
		t.Fatalf("path=%q authorization=%q", path, authorization)
	}
	if providerID != "wamid.outbound" || !strings.Contains(body, `"to":"252611234567"`) || !strings.Contains(body, `"body":"hello"`) || !strings.Contains(body, `"message_id":"wamid.inbound"`) {
		t.Fatalf("body=%q", body)
	}
}

func TestGraphClientBoundsUnknownLengthMedia(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.(http.Flusher).Flush()
		_, _ = response.Write([]byte("12345"))
	}))
	defer server.Close()
	client := NewGraphClient(server.Client(), server.URL, "v22.0", "access-token")
	reader, err := client.DownloadMedia(context.Background(), Media{URL: server.URL}, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	_, err = io.ReadAll(reader)

	if !errors.Is(err, ErrMediaTooLarge) {
		t.Fatalf("err=%v", err)
	}
}
