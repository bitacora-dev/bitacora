package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTelegramNotifier_SendsToBotAPI(t *testing.T) {
	var gotPath string
	var gotReq telegramSendMessageRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Errorf("unexpected error decoding request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(telegramResponse{OK: true})
	}))
	defer server.Close()

	notifier := &TelegramNotifier{BotToken: "TESTTOKEN", ChatID: "12345", apiBaseURL: server.URL}
	if err := notifier.Notify(context.Background(), sampleNotification()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotPath != "/botTESTTOKEN/sendMessage" {
		t.Fatalf("expected path /botTESTTOKEN/sendMessage, got %q", gotPath)
	}
	if gotReq.ChatID != "12345" {
		t.Fatalf("expected chat_id 12345, got %q", gotReq.ChatID)
	}
	if gotReq.Text == "" {
		t.Fatal("expected a non-empty message text")
	}
}

func TestTelegramNotifier_FailsWhenAPIReportsNotOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(telegramResponse{OK: false, Description: "chat not found"})
	}))
	defer server.Close()

	notifier := &TelegramNotifier{BotToken: "t", ChatID: "1", apiBaseURL: server.URL}
	err := notifier.Notify(context.Background(), sampleNotification())
	if err == nil {
		t.Fatal("expected an error when the Telegram API reports ok=false")
	}
	if fmt.Sprint(err) == "" {
		t.Fatal("expected a non-empty error message")
	}
}

func TestTelegramNotifier_FailsOnHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	notifier := &TelegramNotifier{BotToken: "t", ChatID: "1", apiBaseURL: server.URL}
	if err := notifier.Notify(context.Background(), sampleNotification()); err == nil {
		t.Fatal("expected an error for a 500 response")
	}
}
