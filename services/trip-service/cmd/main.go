package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"uber/shared/pkg/db"
	"uber/shared/pkg/jwt"
	"uber/shared/pkg/kafka"
	rredis "uber/shared/pkg/redis"
	"uber/trip-service/config"
	"uber/trip-service/internal/controllers"
	"uber/trip-service/internal/tracking"
	"uber/trip-service/internal/repositories"
	"uber/trip-service/internal/service"
	"uber/trip-service/migrations"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := config.Load()
	if err := jwt.Init(cfg.JWTSecret); err != nil {
		log.Fatal(err)
	}

	pool := db.MustConnect(ctx, cfg.DatabaseURL, migrations.FS)

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
		kafka.TopicRatingSubmitted,
	); err != nil {
		log.Fatal(err)
	}

	repo := repositories.NewRepository(pool)
	svc := service.New(repo, kafkaClient, redisClient)

	// Kafka consumers
	kafkaClient.Subscribe(ctx, kafka.TopicDriverAssigned, "trip-driver-assigned", func(data []byte) error {
		var ev kafka.DriverAssignedEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		log.Printf("[trip] driver.assigned: trip=%s driver=%s", ev.TripID, ev.DriverID)
		return repo.AssignDriver(ctx, ev.TripID, ev.DriverID)
	})

	wsHub := tracking.NewHub()
	h := controllers.New(svc, wsHub)

	r := chi.NewRouter()
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	r.Use(chimw.RealIP)
	r.Use(jwt.OptionalAuth)

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := pool.Ping(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"status":"unhealthy","service":"trip-service"}`))
			return
		}
		if err := redisClient.Ping(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"status":"unhealthy","service":"trip-service"}`))
			return
		}
		w.Write([]byte(`{"status":"ok","service":"trip-service"}`))
	})
	r.Mount("/trips", h.Routes())
	r.Mount("/ws", wsHub.Routes())

	srv := &http.Server{Addr: ":" + cfg.Port, Handler: r}
	go func() {
		log.Printf("trip-service listening on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down trip-service...")

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	srv.Shutdown(shutCtx)
	cancel()
}
