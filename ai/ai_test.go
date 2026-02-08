package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestAgent(t *testing.T) {
	key, url, model := os.Getenv("AIKey"), os.Getenv("AIURL"), os.Getenv("AIModel")
	if key == "" || url == "" || model == "" {
		t.Skip("AI{Key,URL,Model} not set")
	}
	content := "hello world"
	c := &Client{URL: url, Key: key, Model: model}
	tools := []Tool{{
		Name:        "edit",
		Description: "Replace text in a file",
		Schema:      MapStringArgs("path", "find", "replace"),
		Handler: func(_ context.Context, raw json.RawMessage) (string, Block, error) {
			r := struct{ Path, Find, Replace string }{}
			if err := json.Unmarshal(raw, &r); err != nil {
				return "", Block{}, err
			} else if r.Path != "test.txt" {
				return "", Block{}, fmt.Errorf("file %q not found", r.Path)
			} else if !strings.Contains(content, r.Find) {
				return "", Block{}, fmt.Errorf("string %q not found", r.Find)
			}
			content = strings.Replace(content, r.Find, r.Replace, 1)
			return "success", Block{Text: "edit"}, nil
		},
	}}
	ch := NewChat(c, "You have access to a file named 'test.txt'.", tools)
	if err := ch.Loop(t.Context(), "In test.txt, replace 'world' with 'universe'", 10, nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	if content != "hello universe" {
		t.Fatalf("got %q, want 'hello universe'", content)
	}
}
