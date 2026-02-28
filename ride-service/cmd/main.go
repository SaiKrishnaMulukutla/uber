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

	"ride-service/internal/drivers"
	"ride-service/internal/matching"
	"ride-service/internal/tracking"
	"ride-service/internal/trips"
	"ride-service/internal/users"
	"ride-service/migrations"
	"ride-service/pkg/db"
	"ride-service/pkg/jwt"
	"ride-service/pkg/kafka"
	rredis "ride-service/pkg/redis"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ── 1. JWT secret ──
	if err := jwt.Init(env("JWT_SECRET", "")); err != nil {
		log.Fatal(err)
	}

	// ── 2. PostgreSQL ──
	database, err := db.Connect(ctx, env("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/ride_db?sslmode=disable"))
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()

	if err := database.RunMigrations(ctx, migrations.FS); err != nil {
		log.Fatal("migrations failed:", err)
	}

	// ── 3. Redis ──
	redisClient, err := rredis.NewClient(env("REDIS_ADDR", "localhost:6379"))
	if err != nil {
		log.Fatal(err)
	}
	defer redisClient.Close()

	// ── 4. Kafka ──
	brokers := strings.Split(env("KAFKA_BROKERS", "localhost:9092"), ",")
	kafkaClient := kafka.NewClient(brokers)

	if err := kafkaClient.EnsureTopics(ctx,
		kafka.TopicRideRequested,
		kafka.TopicDriverAssigned,
		kafka.TopicTripCompleted,
	); err != nil {
		log.Fatal(err)
	}

	// ── 5. Services ──
	userSvc := users.NewService(database.Pool)
	driverSvc := drivers.NewService(database.Pool, redisClient)
	tripSvc := trips.NewService(database.Pool, kafkaClient, redisClient)

	// ── 6. Background consumers ──
	matcher := matching.NewMatcher(kafkaClient, redisClient)
	matcher.Start(ctx)

	tripSvc.StartDriverAssignedConsumer(ctx)

	// ── 7. WebSocket hub ──
	wsHub := tracking.NewHub()

	// ── 8. HTTP router ──
	r := chi.NewRouter()
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	r.Use(chimw.RealIP)
	r.Use(jwt.OptionalAuth)

	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","service":"ride-service"}`))
	})

	r.Mount("/users", users.NewHandler(userSvc).Routes())
	r.Mount("/drivers", drivers.NewHandler(driverSvc).Routes())
	r.Mount("/trips", trips.NewHandler(tripSvc).Routes())
	r.Mount("/ws", wsHub.Routes())

	// ── 9. Start server ──
	port := env("PORT", "8080")
	srv := &http.Server{Addr: ":" + port, Handler: r}

	go func() {
		log.Printf("ride-service listening on :%s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	// ── 10. Graceful shutdown ──
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down...")

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	srv.Shutdown(shutCtx)
	cancel() // stop consumers
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
