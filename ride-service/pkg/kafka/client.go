package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

// Well-known topic names.
const (
	TopicRideRequested  = "ride.requested"
	TopicDriverAssigned = "driver.assigned"
	TopicTripCompleted  = "trip.completed"
)

// Client wraps Kafka operations.
type Client struct {
	brokers []string
}

// NewClient returns a Client connected to the given brokers.
func NewClient(brokers []string) *Client {
	return &Client{brokers: brokers}
}

// EnsureTopics creates topics if they don't already exist (with retry).
func (c *Client) EnsureTopics(ctx context.Context, topics ...string) error {
	for attempt := 1; attempt <= 20; attempt++ {
		conn, err := kafkago.DialContext(ctx, "tcp", c.brokers[0])
		if err != nil {
			log.Printf("Kafka not ready, retrying in 3s... (%d/20)", attempt)
			time.Sleep(3 * time.Second)
			continue
		}

		configs := make([]kafkago.TopicConfig, len(topics))
		for i, t := range topics {
			configs[i] = kafkago.TopicConfig{
				Topic:             t,
				NumPartitions:     3,
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

// Publish sends a JSON-serialised message to a topic.
func (c *Client) Publish(ctx context.Context, topic, key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	w := &kafkago.Writer{
		Addr:     kafkago.TCP(c.brokers...),
		Topic:    topic,
		Balancer: &kafkago.LeastBytes{},
	}
	defer w.Close()

	return w.WriteMessages(ctx, kafkago.Message{
		Key:   []byte(key),
		Value: data,
	})
}

// Subscribe starts a background goroutine that reads from a topic.
func (c *Client) Subscribe(ctx context.Context, topic, groupID string, handler func([]byte) error) {
	r := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:  c.brokers,
		Topic:    topic,
		GroupID:  groupID,
		MinBytes: 1,
		MaxBytes: 10e6,
	})

	go func() {
		defer r.Close()
		for {
			msg, err := r.ReadMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("[kafka] read error on %s: %v", topic, err)
				time.Sleep(time.Second)
				continue
			}
			if err := handler(msg.Value); err != nil {
				log.Printf("[kafka] handler error on %s: %v", topic, err)
			}
		}
	}()
}
