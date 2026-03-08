package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"uber/notification-service/internal/notifications"
	"uber/notification-service/migrations"
	"uber/shared/pkg/db"
	"uber/shared/pkg/jwt"
	"uber/shared/pkg/kafka"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := jwt.Init(env("JWT_SECRET", "")); err != nil {
		log.Fatal(err)
	}

	database, err := db.Connect(ctx, env("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/notifications_db?sslmode=disable"))
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()

	if err := database.RunMigrations(ctx, migrations.FS); err != nil {
		log.Fatal("migrations failed:", err)
	}

	brokers := strings.Split(env("KAFKA_BROKERS", "localhost:9092"), ",")
	kafkaClient := kafka.NewClient(brokers)

	if err := kafkaClient.EnsureTopics(ctx,
		kafka.TopicRideRequested,
		kafka.TopicDriverAssigned,
		kafka.TopicTripCompleted,
		kafka.TopicTripCancelled,
		kafka.TopicRatingSubmitted,
		kafka.TopicPaymentCompleted,
	); err != nil {
		log.Fatal(err)
	}

	notifSvc := notifications.NewService(database.Pool)
	notifSvc.StartEventConsumers(ctx, kafkaClient)

	r := chi.NewRouter()
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	r.Use(chimw.RealIP)
	r.Use(jwt.OptionalAuth)

	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","service":"notification-service"}`))
	})

	r.Mount("/notifications", notifications.NewHandler(notifSvc).Routes())

	port := env("PORT", "8084")
	srv := &http.Server{Addr: ":" + port, Handler: r}

	go func() {
		log.Printf("notification-service listening on :%s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down notification-service...")

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	srv.Shutdown(shutCtx)
	cancel()
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
