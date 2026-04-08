package main

import (
	"context"
	"log"
	"net/http"
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

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := redisClient.Ping(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"status":"unhealthy","service":"matching-service"}`))
			return
		}
		w.Write([]byte(`{"status":"ok","service":"matching-service"}`))
	})
	go func() {
		log.Printf("matching-service health endpoint on :%s", cfg.Port)
		if err := http.ListenAndServe(":"+cfg.Port, mux); err != nil {
			log.Printf("health server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down matching-service...")
	cancel()
}
