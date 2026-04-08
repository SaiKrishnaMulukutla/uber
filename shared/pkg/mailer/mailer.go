package mailer

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
)

// Mailer sends transactional emails.
type Mailer interface {
	Send(to, subject, body string) error
}

// GmailMailer sends email via SMTP with STARTTLS on port 587.
type GmailMailer struct {
	host string
	port int
	user string
	pass string
}

// New returns a GmailMailer with the given credentials.
func New(host string, port int, user, pass string) Mailer {
	return &GmailMailer{host: host, port: port, user: user, pass: pass}
}

// Send delivers an HTML email to the recipient.
// Port 465 uses direct TLS (SMTPS); any other port uses STARTTLS.
func (m *GmailMailer) Send(to, subject, body string) error {
	addr := net.JoinHostPort(m.host, fmt.Sprintf("%d", m.port))
	tlsCfg := &tls.Config{ServerName: m.host}

	var client *smtp.Client
	if m.port == 465 {
		conn, err := tls.Dial("tcp", addr, tlsCfg)
		if err != nil {
			return fmt.Errorf("mailer: tls dial failed: %w", err)
		}
		c, err := smtp.NewClient(conn, m.host)
		if err != nil {
			return fmt.Errorf("mailer: smtp client failed: %w", err)
		}
		client = c
	} else {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			return fmt.Errorf("mailer: dial failed: %w", err)
		}
		c, err := smtp.NewClient(conn, m.host)
		if err != nil {
			return fmt.Errorf("mailer: smtp client failed: %w", err)
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("mailer: starttls failed: %w", err)
		}
		client = c
	}
	defer client.Close()

	auth := smtp.PlainAuth("", m.user, m.pass, m.host)
	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("mailer: auth failed: %w", err)
	}

	if err := client.Mail(m.user); err != nil {
		return fmt.Errorf("mailer: MAIL FROM failed: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("mailer: RCPT TO failed: %w", err)
	}

	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("mailer: DATA failed: %w", err)
	}
	defer wc.Close()

	msg := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\n"+
			"MIME-Version: 1.0\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n%s",
		m.user, to, subject, body,
	)
	if _, err := fmt.Fprint(wc, msg); err != nil {
		return fmt.Errorf("mailer: write body failed: %w", err)
	}

	return nil
}
