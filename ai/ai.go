package ai

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type Client struct {
	URL, Key, Model string
}

type Chat struct {
	*Client
	System      string
	Messages    []Message
	MaxTokens   int
	Temperature float64
	Tools       []Tool
}

type Message struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type Handler func(context.Context, json.RawMessage) (string, Block, error)

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"input_schema"`
	Handler     `json:"-"`
}

type Block struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

func NewChat(c *Client, system string, tools []Tool, userMsgs ...string) *Chat {
	ms := []Message{}
	for _, s := range userMsgs {
		ms = append(ms, Message{Role: "user", Content: s})
	}
	return &Chat{Client: c, System: system, Tools: tools, Messages: ms, MaxTokens: 4096}
}

func (ch *Chat) Send(ctx context.Context, input any) ([]Block, error) {
	if input != nil {
		ch.Messages = append(ch.Messages, Message{Role: "user", Content: input})
	}
	body, err := json.Marshal(map[string]any{
		"model":       ch.Model,
		"max_tokens":  ch.MaxTokens,
		"system":      ch.System,
		"messages":    ch.Messages,
		"tools":       ch.Tools,
		"temperature": ch.Temperature,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", ch.URL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("req: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", ch.Key)
	req.Header.Set("Anthropic-Version", "2023-06-01")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()
	bs, err := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, string(bs))
	}
	r := struct{ Content []Block }{}
	if err := json.Unmarshal(bs, &r); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	ch.Messages = append(ch.Messages, Message{Role: "assistant", Content: r.Content})
	return r.Content, nil
}

func (ch *Chat) Loop(ctx context.Context, input any, n int, cb func(Block)) error {
	for range n {
		blks, err := ch.Send(ctx, input)
		if err != nil {
			return err
		}
		rblks := []map[string]any{}
		for _, b := range blks {
			if cb != nil {
				cb(b)
			}
			if b.Type != "tool_use" {
				continue
			}
			v, rb, err := "error: tool not found", Block{}, fmt.Errorf("tool not found")
			for _, t := range ch.Tools {
				if t.Name == b.Name {
					v, rb, err = t.Handler(ctx, b.Input)
					break
				}
			}
			if err != nil {
				v = "error: " + err.Error()
			}
			rb.ID, rb.Type = cmp.Or(rb.ID, b.ID), cmp.Or(rb.Type, "tool")
			rb.Name, rb.Text = cmp.Or(rb.Name, b.Name), cmp.Or(rb.Text, v)
			if cb != nil {
				cb(rb)
			}
			rblks = append(rblks, map[string]any{
				"type": "tool_result", "tool_use_id": b.ID, "content": v,
			})
		}
		if len(rblks) == 0 {
			return nil
		}
		input = rblks
	}
	return fmt.Errorf("max steps %d exceeded", n)
}

func MapStringArgs(ks ...string) json.RawMessage {
	m := map[string]any{}
	for _, n := range ks {
		m[n] = map[string]string{"type": "string"}
	}
	bs, _ := json.Marshal(map[string]any{"type": "object", "properties": m, "required": ks})
	return bs
}
