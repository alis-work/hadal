package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

var ErrMediaTooLarge = errors.New("whatsapp media exceeds size limit")

type Media struct {
	ID       string
	URL      string
	MIMEType string
	Size     int64
}

type Client interface {
	ResolveMedia(context.Context, string) (Media, error)
	DownloadMedia(context.Context, Media, int64) (io.ReadCloser, error)
	SendText(context.Context, string, string, string, string) (string, error)
}

type GraphClient struct {
	httpClient *http.Client
	baseURL    string
	version    string
	token      string
}

func NewGraphClient(httpClient *http.Client, baseURL, graphVersion, token string) *GraphClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if baseURL == "" {
		baseURL = "https://graph.facebook.com"
	}
	return &GraphClient{httpClient: httpClient, baseURL: strings.TrimRight(baseURL, "/"), version: strings.Trim(graphVersion, "/"), token: token}
}

func (c *GraphClient) ResolveMedia(ctx context.Context, id string) (Media, error) {
	var response struct {
		ID       string `json:"id"`
		URL      string `json:"url"`
		MIMEType string `json:"mime_type"`
		Size     int64  `json:"file_size"`
	}
	if err := c.doJSON(ctx, http.MethodGet, c.graphURL(id), nil, &response); err != nil {
		return Media{}, err
	}
	return Media{ID: response.ID, URL: response.URL, MIMEType: response.MIMEType, Size: response.Size}, nil
}

func (c *GraphClient) DownloadMedia(ctx context.Context, media Media, maxBytes int64) (io.ReadCloser, error) {
	if maxBytes <= 0 || media.Size > maxBytes {
		return nil, ErrMediaTooLarge
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, media.URL, nil)
	if err != nil {
		return nil, permanent(err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, statusError(response.StatusCode)
	}
	if response.ContentLength > maxBytes {
		response.Body.Close()
		return nil, ErrMediaTooLarge
	}
	return &boundedReadCloser{reader: response.Body, closer: response.Body, remaining: maxBytes}, nil
}

func (c *GraphClient) SendText(ctx context.Context, phoneNumberID, recipient, replyTo, text string) (string, error) {
	payload := map[string]any{"messaging_product": "whatsapp", "recipient_type": "individual", "to": strings.TrimPrefix(recipient, "+"), "type": "text", "text": map[string]string{"body": text}}
	if replyTo != "" {
		payload["context"] = map[string]string{"message_id": replyTo}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", permanent(err)
	}
	var response struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := c.doJSON(ctx, http.MethodPost, c.graphURL(phoneNumberID)+"/messages", bytes.NewReader(body), &response); err != nil {
		return "", err
	}
	if len(response.Messages) != 1 || response.Messages[0].ID == "" {
		return "", permanent(errors.New("graph response omitted message ID"))
	}
	return response.Messages[0].ID, nil
}

func (c *GraphClient) graphURL(path string) string {
	return c.baseURL + "/" + c.version + "/" + url.PathEscape(path)
}

func (c *GraphClient) doJSON(ctx context.Context, method, endpoint string, body io.Reader, target any) error {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return permanent(err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		if method == http.MethodPost && ctx.Err() == nil {
			return ambiguousError{err}
		}
		if ctx.Err() != nil {
			return permanent(ctx.Err())
		}
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return statusError(response.StatusCode)
	}
	if target != nil {
		if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(target); err != nil {
			decodeErr := fmt.Errorf("decode graph response: %w", err)
			if method == http.MethodPost {
				return ambiguousError{decodeErr}
			}
			return permanent(decodeErr)
		}
	}
	return nil
}

type permanentError struct{ error }
type ambiguousError struct{ error }

func permanent(err error) error { return permanentError{err} }

func (e permanentError) Unwrap() error { return e.error }
func (e ambiguousError) Unwrap() error { return e.error }

func IsAmbiguous(err error) bool {
	var target ambiguousError
	return errors.As(err, &target)
}

func IsTransient(err error) bool {
	var target permanentError
	return err != nil && !errors.As(err, &target) && !errors.Is(err, ErrMediaTooLarge)
}

func statusError(status int) error {
	err := fmt.Errorf("graph api returned HTTP %d", status)
	if status == http.StatusTooManyRequests || status >= 500 {
		return err
	}
	return permanent(err)
}

type boundedReadCloser struct {
	reader    io.Reader
	closer    io.Closer
	remaining int64
	checked   bool
}

func (r *boundedReadCloser) Read(buffer []byte) (int, error) {
	if r.remaining > 0 {
		if int64(len(buffer)) > r.remaining {
			buffer = buffer[:r.remaining]
		}
		n, err := r.reader.Read(buffer)
		r.remaining -= int64(n)
		return n, err
	}
	if r.checked {
		return 0, io.EOF
	}
	r.checked = true
	var extra [1]byte
	n, err := r.reader.Read(extra[:])
	if n > 0 {
		return 0, ErrMediaTooLarge
	}
	return 0, err
}

func (r *boundedReadCloser) Close() error { return r.closer.Close() }
