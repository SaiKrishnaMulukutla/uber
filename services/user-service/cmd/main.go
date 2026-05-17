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
	"github.com/go-chi/cors"
	chimw "github.com/go-chi/chi/v5/middleware"

	"uber/shared/pkg/db"
	"uber/shared/pkg/jwt"
	"uber/shared/pkg/kafka"
	"uber/shared/pkg/mailer"
	"uber/shared/pkg/otp"
	rredis "uber/shared/pkg/redis"
	"uber/user-service/config"
	"uber/user-service/internal/controllers"
	"uber/user-service/internal/repositories"
	"uber/user-service/internal/service"
	"uber/user-service/migrations"
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
	if err := kafkaClient.EnsureTopics(ctx, kafka.TopicRideGoEvents); err != nil {
		log.Fatal(err)
	}

	repo := repositories.NewRepository(pool)

	var m mailer.Mailer
	if cfg.EmailUser != "" && cfg.EmailPass != "" {
		smtp := mailer.New(cfg.EmailHost, cfg.EmailPort, cfg.EmailUser, cfg.EmailPass)
		if cfg.BrevoAPIKey != "" {
			m = mailer.NewAsync(mailer.WithFallback(mailer.NewBrevo(cfg.BrevoAPIKey, cfg.EmailUser), smtp), 5)
		} else {
			m = mailer.NewAsync(smtp, 5)
		}
	}

	otpClient := otp.New(redisClient.RDB(), m)
	svc := service.NewService(repo, otpClient, redisClient.RDB(), m)

	// Kafka consumers
	kafkaClient.Subscribe(ctx, kafka.TopicRideGoEvents, "user-ridego-events", func(data []byte) error {
		var env kafka.EventEnvelope
		if err := json.Unmarshal(data, &env); err != nil {
			return err
		}
		if env.Type != kafka.EventTypeRatingSubmitted {
			return nil
		}
		var ev kafka.RatingSubmittedEvent
		if err := json.Unmarshal(env.Payload, &ev); err != nil {
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
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Authorization", "Content-Type", "Accept"},
		AllowCredentials: false,
	}))
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
