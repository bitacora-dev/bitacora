package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

const defaultTelegramAPIBaseURL = "https://api.telegram.org"

// TelegramNotifier sends via a Telegram bot — "práctico y sin
// infraestructura" (ADR-0009).
type TelegramNotifier struct {
	BotToken string
	ChatID   string

	HTTPClient *http.Client
	// apiBaseURL overrides the Telegram API host — tests point it at a
	// local httptest.Server instead of the real api.telegram.org.
	apiBaseURL string
}

type telegramSendMessageRequest struct {
	ChatID string `json:"chat_id"`
	Text   string `json:"text"`
}

type telegramResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
}

// Notify implements Notifier.
func (t *TelegramNotifier) Notify(ctx context.Context, notif Notification) error {
	client := t.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	base := t.apiBaseURL
	if base == "" {
		base = defaultTelegramAPIBaseURL
	}

	body, err := json.Marshal(telegramSendMessageRequest{ChatID: t.ChatID, Text: notif.Body()})
	if err != nil {
		return fmt.Errorf("marshaling telegram payload: %w", err)
	}

	url := fmt.Sprintf("%s/bot%s/sendMessage", base, t.BotToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building telegram request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("sending telegram message: %w", err)
	}
	defer resp.Body.Close()

	var decoded telegramResponse
	_ = json.NewDecoder(resp.Body).Decode(&decoded)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !decoded.OK {
		if decoded.Description != "" {
			return fmt.Errorf("telegram API error: %s", decoded.Description)
		}
		return fmt.Errorf("telegram API returned %s", resp.Status)
	}
	return nil
}
