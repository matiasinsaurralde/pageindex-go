package llm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/joho/godotenv"
	openai "github.com/sashabaranov/go-openai"
)

type Client struct {
	api *openai.Client
}

func NewFromEnv() (*Client, error) {
	_ = godotenv.Load()
	key := os.Getenv("CHATGPT_API_KEY")
	if key == "" {
		key = os.Getenv("OPENAI_API_KEY")
	}
	if key == "" {
		return nil, errors.New("missing CHATGPT_API_KEY or OPENAI_API_KEY")
	}
	return &Client{
		api: openai.NewClient(key),
	}, nil
}

func (c *Client) Chat(ctx context.Context, model string, prompt string, history []openai.ChatCompletionMessage) (string, error) {
	content, _, err := c.ChatWithFinishReason(ctx, model, prompt, history)
	return content, err
}

func (c *Client) ChatWithFinishReason(ctx context.Context, model string, prompt string, history []openai.ChatCompletionMessage) (string, string, error) {
	messages := make([]openai.ChatCompletionMessage, 0, len(history)+1)
	messages = append(messages, history...)
	messages = append(messages, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: prompt,
	})

	var lastErr error
	for attempt := 0; attempt < 10; attempt++ {
		resp, err := c.api.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
			Model:       model,
			Messages:    messages,
			Temperature: float32(0),
		})
		if err == nil && len(resp.Choices) > 0 {
			content := resp.Choices[0].Message.Content
			finishReason := string(resp.Choices[0].FinishReason)
			return content, finishReason, nil
		}
		if err == nil {
			err = errors.New("openai returned no choices")
		}
		lastErr = err
		backoff := time.Duration(attempt+1) * 300 * time.Millisecond
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case <-time.After(backoff):
		}
	}

	return "", "", fmt.Errorf("openai chat failed after retries: %w", lastErr)
}

func (c *Client) Close() error {
	return nil
}
