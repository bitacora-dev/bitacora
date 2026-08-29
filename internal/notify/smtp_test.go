package notify

import (
	"context"
	"errors"
	"net/smtp"
	"strings"
	"testing"
)

func TestSMTPNotifier_SendsWithCorrectAddrAndRecipients(t *testing.T) {
	var gotAddr, gotFrom string
	var gotTo []string
	var gotMsg []byte

	notifier := &SMTPNotifier{
		Host: "smtp.example.invalid", Port: 587, Username: "user", Password: "pass",
		From: "bitacora@example.invalid", To: []string{"ops@example.invalid"},
		sendMail: func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
			gotAddr, gotFrom, gotTo, gotMsg = addr, from, to, msg
			return nil
		},
	}

	if err := notifier.Notify(context.Background(), sampleNotification()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotAddr != "smtp.example.invalid:587" {
		t.Fatalf("expected addr smtp.example.invalid:587, got %q", gotAddr)
	}
	if gotFrom != "bitacora@example.invalid" {
		t.Fatalf("expected from bitacora@example.invalid, got %q", gotFrom)
	}
	if len(gotTo) != 1 || gotTo[0] != "ops@example.invalid" {
		t.Fatalf("expected to=[ops@example.invalid], got %+v", gotTo)
	}
	if !strings.Contains(string(gotMsg), "Subject:") || !strings.Contains(string(gotMsg), "cpu-temp-alta") {
		t.Fatalf("expected the message to contain a subject and the rule id, got %q", gotMsg)
	}
}

func TestSMTPNotifier_PropagatesSendError(t *testing.T) {
	notifier := &SMTPNotifier{
		Host: "smtp.example.invalid", Port: 587, From: "a@example.invalid", To: []string{"b@example.invalid"},
		sendMail: func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
			return errors.New("connection refused")
		},
	}
	if err := notifier.Notify(context.Background(), sampleNotification()); err == nil {
		t.Fatal("expected the underlying send error to propagate")
	}
}

func TestSMTPNotifier_RejectsNoRecipients(t *testing.T) {
	notifier := &SMTPNotifier{Host: "smtp.example.invalid", Port: 587, From: "a@example.invalid"}
	if err := notifier.Notify(context.Background(), sampleNotification()); err == nil {
		t.Fatal("expected an error when there are no recipients configured")
	}
}

func TestNewSMTPNotifier_UsesRealSendMail(t *testing.T) {
	n := NewSMTPNotifier("smtp.example.invalid", 587, "u", "p", "a@example.invalid", []string{"b@example.invalid"})
	if n.sendMail == nil {
		t.Fatal("expected NewSMTPNotifier to wire a real sendMail function")
	}
}
