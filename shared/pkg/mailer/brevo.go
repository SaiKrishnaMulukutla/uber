package mailer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

type brevoMailer struct {
	apiKey      string
	senderEmail string
}

// NewBrevo returns a Mailer that delivers email via Brevo's HTTP API.
// No SMTP ports required — works on any host including Render.
func NewBrevo(apiKey, senderEmail string) Mailer {
	return &brevoMailer{apiKey: apiKey, senderEmail: senderEmail}
}

func (b *brevoMailer) Send(to, subject, body string) error {
	payload := map[string]any{
		"sender":      map[string]string{"email": b.senderEmail},
		"to":          []map[string]string{{"email": to}},
		"subject":     subject,
		"htmlContent": body,
	}

	buf, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("brevo: marshal failed: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, "https://api.brevo.com/v3/smtp/email", bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("brevo: build request failed: %w", err)
	}
	req.Header.Set("api-key", b.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("brevo: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("brevo: unexpected status %d", resp.StatusCode)
	}
	return nil
}
