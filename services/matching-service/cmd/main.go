package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"uber/matching-service/config"
	"uber/matching-service/internal/service"
	"uber/shared/pkg/kafka"
	rredis "uber/shared/pkg/redis"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := config.Load()

	// ready is set to 1 once Redis + Kafka are connected.
	var ready atomic.Int32

	// Health endpoint starts immediately so Render's health check passes.
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if ready.Load() == 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"status":"starting","service":"matching-service"}`))
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

	// Connect to Redis + Kafka in background so the HTTP server is up instantly.
	go func() {
		var redisClient *rredis.Client
		for {
			rc, err := rredis.NewClient(cfg.RedisAddr)
			if err == nil {
				redisClient = rc
				break
			}
			log.Printf("Redis unavailable, retrying in 10s: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Second):
			}
		}
		kafkaClient := kafka.NewClient(cfg.KafkaBrokers)
		if err := kafkaClient.EnsureTopics(ctx,
			kafka.TopicRideRequested,
			kafka.TopicDriverAssigned,
			kafka.TopicTripCompleted,
			kafka.TopicTripCancelled,
		); err != nil {
			log.Fatalf("kafka topics: %v", err)
		}

		matcher := service.NewMatcher(kafkaClient, redisClient)
		ready.Store(1)
		log.Println("matching-service ready, consuming ride.requested events...")
		kafkaClient.Subscribe(ctx, kafka.TopicRideRequested, "matching-group", func(data []byte) error {
			return matcher.HandleRideRequested(ctx, data)
		})
		// Keep Redis open until shutdown — Subscribe runs in its own goroutine.
		<-ctx.Done()
		redisClient.Close()
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down matching-service...")
	cancel()
}
