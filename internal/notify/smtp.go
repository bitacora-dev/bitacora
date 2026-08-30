package notify

import (
	"context"
	"fmt"
	"net/smtp"
	"strings"
)

// SMTPNotifier sends alert email — "supervivencia" (ADR-0009): the
// channel that still works when everything self-hosted is down.
type SMTPNotifier struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	To       []string

	// sendMail is injected so tests don't need a real SMTP server —
	// defaults to smtp.SendMail. See NewSMTPNotifier.
	sendMail func(addr string, a smtp.Auth, from string, to []string, msg []byte) error
}

// NewSMTPNotifier returns a notifier that sends through a real SMTP
// server via net/smtp.
func NewSMTPNotifier(host string, port int, username, password, from string, to []string) *SMTPNotifier {
	return &SMTPNotifier{
		Host: host, Port: port, Username: username, Password: password, From: from, To: to,
		sendMail: smtp.SendMail,
	}
}

// Notify implements Notifier.
func (s *SMTPNotifier) Notify(ctx context.Context, notif Notification) error {
	if len(s.To) == 0 {
		return fmt.Errorf("smtp notifier has no recipients configured")
	}

	msg := buildEmailMessage(s.From, s.To, notif.Title(), notif.Body())
	auth := smtp.PlainAuth("", s.Username, s.Password, s.Host)
	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)

	if err := s.sendMail(addr, auth, s.From, s.To, msg); err != nil {
		return fmt.Errorf("sending email via %s: %w", addr, err)
	}
	return nil
}

func buildEmailMessage(from string, to []string, subject, body string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	return []byte(b.String())
}
