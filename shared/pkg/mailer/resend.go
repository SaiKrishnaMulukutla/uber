package mailer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

type resendMailer struct {
	apiKey string
	from   string
}

// NewResend returns a Mailer that delivers via the Resend HTTP API.
func NewResend(apiKey, from string) Mailer {
	return &resendMailer{apiKey: apiKey, from: from}
}

type resendPayload struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html"`
}

func (m *resendMailer) Send(to, subject, body string) error {
	payload, err := json.Marshal(resendPayload{
		From:    m.from,
		To:      []string{to},
		Subject: subject,
		HTML:    body,
	})
	if err != nil {
		return fmt.Errorf("mailer: marshal: %w", err)
	}

	req, err := http.NewRequest("POST", "https://api.resend.com/emails", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("mailer: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+m.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("mailer: http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("mailer: resend returned %d", resp.StatusCode)
	}
	return nil
}
