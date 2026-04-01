package mailer

import (
	sharedmailer "uber/shared/pkg/mailer"
	"uber/otp-service/config"
)

// Mailer is the shared email sending interface.
type Mailer = sharedmailer.Mailer

// New returns a Mailer configured from the given Config.
func New(cfg config.Config) Mailer {
	return sharedmailer.New(cfg.EmailHost, cfg.EmailPort, cfg.EmailUser, cfg.EmailPass)
}

// NewAsync wraps a Mailer in a buffered worker pool.
func NewAsync(m Mailer, workers int) Mailer {
	return sharedmailer.NewAsync(m, workers)
}
