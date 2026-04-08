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

	"uber/driver-service/config"
	"uber/driver-service/internal/controllers"
	"uber/driver-service/internal/model"
	"uber/shared/pkg/otp"
	"uber/driver-service/internal/repositories"
	"uber/driver-service/internal/service"
	"uber/driver-service/migrations"
	"uber/shared/pkg/db"
	"uber/shared/pkg/jwt"
	"uber/shared/pkg/kafka"
	"uber/shared/pkg/mailer"
	rredis "uber/shared/pkg/redis"
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

	var m mailer.Mailer
	if cfg.EmailUser != "" && cfg.EmailPass != "" {
		m = mailer.NewAsync(mailer.New(cfg.EmailHost, cfg.EmailPort, cfg.EmailUser, cfg.EmailPass), 5)
	}

	otpClient := otp.New(redisClient.RDB(), m)
	svc := service.New(repo, redisClient, otpClient, m)

	// Kafka consumers
	kafkaClient.Subscribe(ctx, kafka.TopicDriverAssigned, "driver-status-assigned", func(data []byte) error {
		var ev kafka.DriverAssignedEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		log.Printf("[driver] driver.assigned: driver=%s trip=%s → status=busy", ev.DriverID, ev.TripID)
		return repo.UpdateStatus(ctx, ev.DriverID, model.StatusBusy)
	})

	kafkaClient.Subscribe(ctx, kafka.TopicTripCompleted, "driver-status-completed", func(data []byte) error {
		var ev kafka.TripCompletedEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		if ev.DriverID == "" {
			return nil
		}
		log.Printf("[driver] trip.completed: driver=%s → status=available", ev.DriverID)
		return repo.UpdateStatus(ctx, ev.DriverID, model.StatusAvailable)
	})

	kafkaClient.Subscribe(ctx, kafka.TopicTripCancelled, "driver-status-cancelled", func(data []byte) error {
		var ev kafka.TripCancelledEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		if ev.DriverID == "" {
			return nil
		}
		log.Printf("[driver] trip.cancelled: driver=%s → status=available", ev.DriverID)
		return repo.UpdateStatus(ctx, ev.DriverID, model.StatusAvailable)
	})

	kafkaClient.Subscribe(ctx, kafka.TopicRatingSubmitted, "driver-rating-update", func(data []byte) error {
		var ev kafka.RatingSubmittedEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		if ev.RateeRole != model.RoleDriver {
			return nil
		}
		log.Printf("[driver] rating.submitted: driver=%s score=%d", ev.RateeID, ev.Score)
		return repo.UpdateRating(ctx, ev.RateeID, ev.Score)
	})

	h := controllers.New(svc)

	r := chi.NewRouter()
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	r.Use(chimw.RealIP)
	r.Use(jwt.OptionalAuth)

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := pool.Ping(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"status":"unhealthy","service":"driver-service"}`))
			return
		}
		if err := redisClient.Ping(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"status":"unhealthy","service":"driver-service"}`))
			return
		}
		w.Write([]byte(`{"status":"ok","service":"driver-service"}`))
	})
	r.Mount("/drivers", h.Routes())

	srv := &http.Server{Addr: ":" + cfg.Port, Handler: r}
	go func() {
		log.Printf("driver-service listening on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down driver-service...")

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	srv.Shutdown(shutCtx)
	cancel()
}
