package kafka

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl/scram"
)

// Well-known topic names.
const (
	TopicRideRequested  = "ride.requested"
	TopicRideOffered    = "ride.offered"
	TopicDriverAssigned = "driver.assigned"
	TopicTripCompleted  = "trip.completed"
	TopicRideGoEvents   = "ridego.events" // trip.cancelled + rating.submitted + payment.completed
)

// Client wraps Kafka operations.
type Client struct {
	brokers   []string
	dialer    *kafkago.Dialer
	transport kafkago.RoundTripper
	writers   sync.Map // topic -> *kafkago.Writer
}

// buildTLSConfig returns a TLS config for Aiven Kafka.
// If KAFKA_CA_CERT is set (base64-encoded PEM), it is loaded into a custom
// cert pool so the broker identity is fully verified.
// Falls back to InsecureSkipVerify only when the env var is absent — this
// preserves existing local-dev behaviour while enforcing verification in prod.
func buildTLSConfig() *tls.Config {
	caB64 := os.Getenv("KAFKA_CA_CERT")
	if caB64 == "" {
		return &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true, //nolint:gosec // local dev only, no CA cert configured
		}
	}
	caBytes, err := base64.StdEncoding.DecodeString(caB64)
	if err != nil {
		log.Fatalf("[kafka] failed to base64-decode KAFKA_CA_CERT: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caBytes) {
		log.Fatalf("[kafka] KAFKA_CA_CERT contains no valid PEM certificates")
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    pool,
	}
}

// NewClient returns a Kafka client. If KAFKA_USERNAME and KAFKA_PASSWORD env
// vars are set it automatically uses SASL SCRAM-SHA-256 over TLS (Aiven /
// Redpanda / Confluent). Otherwise it connects plainly for local dev.
func NewClient(brokers []string) *Client {
	username := os.Getenv("KAFKA_USERNAME")
	password := os.Getenv("KAFKA_PASSWORD")
	if username != "" && password != "" {
		mechanism, err := scram.Mechanism(scram.SHA256, username, password)
		if err != nil {
			log.Fatalf("[kafka] SASL mechanism init failed: %v", err)
		}
		tlsCfg := buildTLSConfig()
		return &Client{
			brokers: brokers,
			dialer: &kafkago.Dialer{
				SASLMechanism: mechanism,
				TLS:           tlsCfg,
			},
			transport: &kafkago.Transport{
				SASL: mechanism,
				TLS:  tlsCfg,
			},
		}
	}
	return &Client{brokers: brokers, dialer: kafkago.DefaultDialer}
}

// getWriter returns a cached writer for the topic, creating one if needed.
func (c *Client) getWriter(topic string) *kafkago.Writer {
	if v, ok := c.writers.Load(topic); ok {
		return v.(*kafkago.Writer)
	}
	w := &kafkago.Writer{
		Addr:      kafkago.TCP(c.brokers...),
		Topic:     topic,
		Balancer:  &kafkago.LeastBytes{},
		Transport: c.transport,
	}
	actual, _ := c.writers.LoadOrStore(topic, w)
	if actual != w {
		w.Close()
	}
	return actual.(*kafkago.Writer)
}

// Close shuts down all cached writers.
func (c *Client) Close() {
	c.writers.Range(func(key, value any) bool {
		value.(*kafkago.Writer).Close()
		return true
	})
}

// WarmWriters pre-creates writers for the given topics so the first Publish
// call does not pay the TLS/SASL connection cost under a request timeout.
func (c *Client) WarmWriters(topics ...string) {
	for _, t := range topics {
		c.getWriter(t)
	}
}

// EnsureTopics creates topics if they don't already exist (with retry).
func (c *Client) EnsureTopics(ctx context.Context, topics ...string) error {
	for attempt := 1; attempt <= 20; attempt++ {
		conn, err := c.dialer.DialContext(ctx, "tcp", c.brokers[0])
		if err != nil {
			log.Printf("Kafka not ready, retrying in 3s... (%d/20)", attempt)
			time.Sleep(3 * time.Second)
			continue
		}

		configs := make([]kafkago.TopicConfig, len(topics))
		for i, t := range topics {
			configs[i] = kafkago.TopicConfig{
				Topic:             t,
				NumPartitions:     1,
				ReplicationFactor: 1,
			}
		}

		err = conn.CreateTopics(configs...)
		conn.Close()
		if err != nil {
			log.Printf("Topic creation returned (may already exist): %v", err)
		}
		log.Println("Kafka topics ensured")
		return nil
	}
	return fmt.Errorf("kafka: could not connect after 20 attempts")
}

func (c *Client) Publish(ctx context.Context, topic, key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return c.getWriter(topic).WriteMessages(writeCtx, kafkago.Message{
		Key:   []byte(key),
		Value: data,
	})
}

// Subscribe starts a background goroutine that reads from a topic.
// Messages are only committed after the handler succeeds; on handler error
// the message is not committed and will be redelivered.
func (c *Client) Subscribe(ctx context.Context, topic, groupID string, handler func([]byte) error) {
	r := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:  c.brokers,
		Topic:    topic,
		GroupID:  groupID,
		Dialer:   c.dialer,
		MinBytes: 1,
		MaxBytes: 10e6,
	})

	go func() {
		defer r.Close()
		for {
			msg, err := r.FetchMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("[kafka] read error on %s: %v", topic, err)
				time.Sleep(time.Second)
				continue
			}

			// Per-message recover: a panic commits (skips) the bad message but keeps the consumer alive.
			func() {
				defer func() {
					if p := recover(); p != nil {
						log.Printf("[kafka] panic processing message on %s (skipping): %v", topic, p)
						if err := r.CommitMessages(ctx, msg); err != nil {
							log.Printf("[kafka] commit error after panic on %s: %v", topic, err)
						}
					}
				}()
				if err := handler(msg.Value); err != nil {
					log.Printf("[kafka] handler error on %s: %v", topic, err)
					return
				}
				if err := r.CommitMessages(ctx, msg); err != nil {
					log.Printf("[kafka] commit error on %s: %v", topic, err)
				}
			}()
		}
	}()
}
