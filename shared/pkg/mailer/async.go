package mailer

import "log"

type sendJob struct {
	to      string
	subject string
	body    string
}

type asyncMailer struct {
	work chan sendJob
}

// NewAsync wraps a Mailer in a buffered worker pool.
// Send returns immediately — workers deliver the email in the background.
// If all workers are busy and the buffer is full, the send is dropped and logged.
func NewAsync(m Mailer, workers int) Mailer {
	am := &asyncMailer{work: make(chan sendJob, workers*10)}
	for i := 0; i < workers; i++ {
		go func() {
			for j := range am.work {
				if err := m.Send(j.to, j.subject, j.body); err != nil {
					log.Printf("[mailer] failed to send to %s: %v", j.to, err)
				}
			}
		}()
	}
	return am
}

// Send enqueues the job and returns immediately (non-blocking).
func (am *asyncMailer) Send(to, subject, body string) error {
	select {
	case am.work <- sendJob{to, subject, body}:
	default:
		log.Printf("[mailer] worker pool full, dropping email to %s", to)
	}
	return nil
}
