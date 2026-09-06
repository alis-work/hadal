package whatsapp

import "testing"

func TestParsePayloadPreservesUnsupportedMedia(t *testing.T) {
	messages, err := ParsePayload([]byte(`{"entry":[{"changes":[{"value":{"messages":[{"id":"wamid.image","from":"252611234567","type":"image","image":{"id":"image-id","mime_type":"image/jpeg"}}]}}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Type != "image" || messages[0].MediaID != "image-id" || messages[0].MIMEType != "image/jpeg" {
		t.Fatalf("messages=%+v", messages)
	}
}
