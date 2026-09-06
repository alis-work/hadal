package whatsapp

import (
	"encoding/json"
	"fmt"
	"strings"
)

type cloudPayload struct {
	Entry []struct {
		Changes []struct {
			Value struct {
				Messages []struct {
					ID   string `json:"id"`
					From string `json:"from"`
					Type string `json:"type"`
					Text struct {
						Body string `json:"body"`
					} `json:"text"`
					Audio struct {
						ID       string `json:"id"`
						MIMEType string `json:"mime_type"`
					} `json:"audio"`
					Image struct {
						ID       string `json:"id"`
						MIMEType string `json:"mime_type"`
					} `json:"image"`
					Video struct {
						ID       string `json:"id"`
						MIMEType string `json:"mime_type"`
					} `json:"video"`
					Document struct {
						ID       string `json:"id"`
						MIMEType string `json:"mime_type"`
					} `json:"document"`
					Sticker struct {
						ID       string `json:"id"`
						MIMEType string `json:"mime_type"`
					} `json:"sticker"`
				} `json:"messages"`
				Statuses []json.RawMessage `json:"statuses"`
			} `json:"value"`
		} `json:"changes"`
	} `json:"entry"`
}

func ParsePayload(body []byte) ([]InboundMessage, error) {
	var payload cloudPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode whatsapp payload: %w", err)
	}
	var result []InboundMessage
	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			for _, message := range change.Value.Messages {
				sender, err := normalizeSender(message.From)
				if err != nil || message.ID == "" || message.Type == "" {
					return nil, fmt.Errorf("invalid whatsapp message identity")
				}
				item := InboundMessage{ProviderMessageID: message.ID, Sender: sender, Type: message.Type}
				switch message.Type {
				case "text":
					item.Text = message.Text.Body
				case "audio":
					item.MediaID = message.Audio.ID
					item.MIMEType = message.Audio.MIMEType
				case "image":
					item.MediaID = message.Image.ID
					item.MIMEType = message.Image.MIMEType
				case "video":
					item.MediaID = message.Video.ID
					item.MIMEType = message.Video.MIMEType
				case "document":
					item.MediaID = message.Document.ID
					item.MIMEType = message.Document.MIMEType
				case "sticker":
					item.MediaID = message.Sticker.ID
					item.MIMEType = message.Sticker.MIMEType
				default:
					item.Type = "unsupported"
				}
				result = append(result, item)
			}
		}
	}
	return result, nil
}

func normalizeSender(value string) (string, error) {
	digits := strings.TrimPrefix(strings.TrimSpace(value), "+")
	if digits == "" {
		return "", fmt.Errorf("empty sender")
	}
	for _, character := range digits {
		if character < '0' || character > '9' {
			return "", fmt.Errorf("invalid sender")
		}
	}
	return "+" + digits, nil
}
