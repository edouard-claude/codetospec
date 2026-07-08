// Package llm provides a minimal OpenAI-compatible chat completions client
// and the repair loop shared by the map and reduce phases.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Message is one chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Usage reports token consumption for one call.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// Add accumulates another usage into u.
func (u *Usage) Add(other Usage) {
	u.PromptTokens += other.PromptTokens
	u.CompletionTokens += other.CompletionTokens
}

// Chatter is the minimal chat interface consumed by the pipeline; *Client
// implements it and tests substitute a local mock.
type Chatter interface {
	Chat(ctx context.Context, msgs []Message) (string, Usage, error)
}

// Client talks to any OpenAI-compatible chat completions endpoint.
type Client struct {
	baseURL    string
	apiKey     string
	model      string
	maxTokens  int
	httpClient *http.Client
}

// NewClient builds a client for one endpoint/model pair.
func NewClient(baseURL, apiKey, model string, maxTokens int) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		model:      model,
		maxTokens:  maxTokens,
		httpClient: &http.Client{Timeout: 180 * time.Second},
	}
}

type chatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
	MaxTokens   int       `json:"max_tokens"`
}

type chatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
}

var retryBackoffs = []time.Duration{2 * time.Second, 8 * time.Second, 20 * time.Second}

// Chat posts the messages and returns the assistant reply content and token
// usage. Network errors, 5xx and 429 responses are retried three times with
// increasing backoff.
func (c *Client) Chat(ctx context.Context, msgs []Message) (string, Usage, error) {
	body, err := json.Marshal(chatRequest{
		Model:       c.model,
		Messages:    msgs,
		Temperature: 0.1,
		MaxTokens:   c.maxTokens,
	})
	if err != nil {
		return "", Usage{}, fmt.Errorf("encode chat request: %w", err)
	}

	var lastErr error
	for attempt := 0; ; attempt++ {
		content, usage, retryable, callErr := c.call(ctx, body)
		if callErr == nil {
			return content, usage, nil
		}
		lastErr = callErr
		if !retryable || attempt >= len(retryBackoffs) {
			return "", Usage{}, lastErr
		}
		select {
		case <-ctx.Done():
			return "", Usage{}, ctx.Err()
		case <-time.After(retryBackoffs[attempt]):
		}
	}
}

func (c *Client) call(ctx context.Context, body []byte) (string, Usage, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", Usage{}, false, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", Usage{}, false, ctx.Err()
		}
		return "", Usage{}, true, fmt.Errorf("chat request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return "", Usage{}, true, fmt.Errorf("read chat response: %w", err)
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return "", Usage{}, true, fmt.Errorf("chat endpoint returned %s: %s", resp.Status, truncate(data, 200))
	}
	if resp.StatusCode != http.StatusOK {
		return "", Usage{}, false, fmt.Errorf("chat endpoint returned %s: %s", resp.Status, truncate(data, 200))
	}

	var parsed chatResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", Usage{}, false, fmt.Errorf("decode chat response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", Usage{}, false, fmt.Errorf("chat response has no choices")
	}
	return parsed.Choices[0].Message.Content, parsed.Usage, false, nil
}

func truncate(data []byte, n int) string {
	s := strings.TrimSpace(string(data))
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// StripFences trims whitespace and surrounding markdown code fences from an
// LLM reply, before JSON unmarshalling.
func StripFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	} else {
		return ""
	}
	if j := strings.LastIndex(s, "```"); j >= 0 {
		s = s[:j]
	}
	return strings.TrimSpace(s)
}

// ChatJSON drives a chat conversation until validate accepts the assistant
// reply, allowing up to maxRepairs correction rounds. Each rejection is fed
// back to the model verbatim. onUsage is invoked after every call.
func ChatJSON[T any](
	ctx context.Context,
	chat Chatter,
	msgs []Message,
	maxRepairs int,
	onUsage func(Usage),
	validate func(reply string) (T, error),
) (T, error) {
	var zero T
	conversation := append([]Message(nil), msgs...)
	var lastErr error
	for round := 0; round <= maxRepairs; round++ {
		reply, usage, err := chat.Chat(ctx, conversation)
		if onUsage != nil {
			onUsage(usage)
		}
		if err != nil {
			return zero, err
		}
		result, validationErr := validate(reply)
		if validationErr == nil {
			return result, nil
		}
		lastErr = validationErr
		conversation = append(conversation,
			Message{Role: "assistant", Content: reply},
			Message{Role: "user", Content: fmt.Sprintf("output rejected: %v; resend the full corrected JSON only", validationErr)},
		)
	}
	return zero, fmt.Errorf("rejected after %d corrections: %w", maxRepairs, lastErr)
}
