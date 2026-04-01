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

	"uber/payment-service/config"
	"uber/payment-service/internal/controllers"
	"uber/payment-service/internal/repositories"
	"uber/payment-service/internal/service"
	"uber/payment-service/migrations"
	"uber/shared/pkg/db"
	"uber/shared/pkg/jwt"
	"uber/shared/pkg/kafka"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := config.Load()
	if err := jwt.Init(cfg.JWTSecret); err != nil {
		log.Fatal(err)
	}

	pool := db.MustConnect(ctx, cfg.DatabaseURL, migrations.FS)

	kafkaClient := kafka.NewClient(cfg.KafkaBrokers)
	if err := kafkaClient.EnsureTopics(ctx,
		kafka.TopicTripCompleted,
		kafka.TopicPaymentCompleted,
	); err != nil {
		log.Fatal(err)
	}

	repo := repositories.NewRepository(pool)
	svc := service.NewService(repo, kafkaClient)

	// Kafka consumers
	kafkaClient.Subscribe(ctx, kafka.TopicTripCompleted, "payment-trip-completed", func(data []byte) error {
		var ev kafka.TripCompletedEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		log.Printf("[payments] trip.completed: trip=%s fare=%.2f", ev.TripID, ev.Fare)
		_, err := svc.CreatePayment(ctx, ev.TripID, ev.RiderID, ev.RiderEmail, ev.DriverID, ev.Fare)
		return err
	})

	h := controllers.NewHandler(svc)

	r := chi.NewRouter()
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	r.Use(chimw.RealIP)
	r.Use(jwt.OptionalAuth)

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := pool.Ping(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"status":"unhealthy","service":"payment-service"}`))
			return
		}
		w.Write([]byte(`{"status":"ok","service":"payment-service"}`))
	})
	r.Mount("/payments", h.Routes())

	srv := &http.Server{Addr: ":" + cfg.Port, Handler: r}
	go func() {
		log.Printf("payment-service listening on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down payment-service...")

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	srv.Shutdown(shutCtx)
	cancel()
}
