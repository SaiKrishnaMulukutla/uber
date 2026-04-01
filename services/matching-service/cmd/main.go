package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"uber/matching-service/config"
	"uber/matching-service/internal/service"
	"uber/shared/pkg/kafka"
	rredis "uber/shared/pkg/redis"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := config.Load()

	redisClient, err := rredis.NewClient(cfg.RedisAddr)
	if err != nil {
		log.Fatal(err)
	}
	defer redisClient.Close()

	kafkaClient := kafka.NewClient(cfg.KafkaBrokers)
	if err := kafkaClient.EnsureTopics(ctx,
		kafka.TopicRideRequested,
		kafka.TopicDriverAssigned,
		kafka.TopicTripCompleted,
		kafka.TopicTripCancelled,
	); err != nil {
		log.Fatal(err)
	}

	matcher := service.NewMatcher(kafkaClient, redisClient)

	log.Println("matching-service started, waiting for ride.requested events...")
	kafkaClient.Subscribe(ctx, kafka.TopicRideRequested, "matching-group", func(data []byte) error {
		return matcher.HandleRideRequested(ctx, data)
	})

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down matching-service...")
	cancel()
}
