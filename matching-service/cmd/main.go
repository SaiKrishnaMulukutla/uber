package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"uber/matching-service/internal/matching"
	"uber/shared/pkg/kafka"
	rredis "uber/shared/pkg/redis"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	redisClient, err := rredis.NewClient(env("REDIS_ADDR", "localhost:6379"))
	if err != nil {
		log.Fatal(err)
	}
	defer redisClient.Close()

	brokers := strings.Split(env("KAFKA_BROKERS", "localhost:9092"), ",")
	kafkaClient := kafka.NewClient(brokers)

	if err := kafkaClient.EnsureTopics(ctx,
		kafka.TopicRideRequested,
		kafka.TopicDriverAssigned,
		kafka.TopicTripCompleted,
		kafka.TopicTripCancelled,
	); err != nil {
		log.Fatal(err)
	}

	matcher := matching.NewMatcher(kafkaClient, redisClient)
	matcher.Start(ctx)

	log.Println("matching-service started, waiting for ride.requested events...")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down matching-service...")
	cancel()
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
