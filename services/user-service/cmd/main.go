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
	"uber/shared/pkg/mailer"
	"uber/user-service/internal/otpclient"
	"uber/user-service/config"
	"uber/user-service/internal/controllers"
	"uber/user-service/migrations"
	"uber/user-service/internal/repositories"
	"uber/user-service/internal/service"
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
	if err := kafkaClient.EnsureTopics(ctx, kafka.TopicRatingSubmitted); err != nil {
		log.Fatal(err)
	}

	repo := repositories.NewRepository(pool)
	otpClient := otpclient.New(cfg.OTPServiceURL)

	var m mailer.Mailer
	if cfg.EmailUser != "" && cfg.EmailPass != "" {
		m = mailer.NewAsync(mailer.New(cfg.EmailHost, cfg.EmailPort, cfg.EmailUser, cfg.EmailPass), 5)
	}

	svc := service.NewService(repo, otpClient, m)

	// Kafka consumers
	kafkaClient.Subscribe(ctx, kafka.TopicRatingSubmitted, "user-rating-update", func(data []byte) error {
		var ev kafka.RatingSubmittedEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		if ev.RateeRole != "rider" {
			return nil
		}
		log.Printf("[users] rating.submitted: rider=%s score=%d", ev.RateeID, ev.Score)
		return repo.UpdateRating(ctx, ev.RateeID, ev.Score)
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
			w.Write([]byte(`{"status":"unhealthy","service":"user-service"}`))
			return
		}
		w.Write([]byte(`{"status":"ok","service":"user-service"}`))
	})
	r.Mount("/users", h.Routes())

	srv := &http.Server{Addr: ":" + cfg.Port, Handler: r}
	go func() {
		log.Printf("user-service listening on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down user-service...")

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	srv.Shutdown(shutCtx)
	cancel()
}
