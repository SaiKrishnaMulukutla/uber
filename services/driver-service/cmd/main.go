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
		kafka.TopicRideOffered,
		kafka.TopicDriverAssigned,
		kafka.TopicTripCompleted,
		kafka.TopicRideGoEvents,
	); err != nil {
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
	svc := service.New(repo, redisClient, otpClient, m, kafkaClient)

	// Startup reconciliation: re-add any available drivers whose Redis GEO entry was
	// lost (e.g. crash mid-operation, missed Kafka message, partition rebalance).
	if availableIDs, recErr := repo.GetAvailableDriverIDs(ctx); recErr == nil {
		restored := 0
		for _, id := range availableIDs {
			lat, lng, locErr := redisClient.GetDriverLocation(ctx, id)
			if locErr != nil {
				continue // no saved location — driver will re-register on next UpdateLocation
			}
			if setErr := redisClient.SetDriverLocation(ctx, id, lat, lng); setErr == nil {
				restored++
			}
		}
		log.Printf("[driver] GEO reconciliation: checked %d available drivers, restored %d to GEO set", len(availableIDs), restored)
	} else {
		log.Printf("[driver] warn: GEO reconciliation skipped: %v", recErr)
	}

	// Kafka consumers
	kafkaClient.Subscribe(ctx, kafka.TopicDriverAssigned, "driver-status-assigned", func(data []byte) error {
		var ev kafka.DriverAssignedEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		log.Printf("[driver] driver.assigned: driver=%s trip=%s → status=busy", ev.DriverID, ev.TripID)
		return repo.UpdateStatus(ctx, ev.DriverID, model.StatusBusy)
	})

	kafkaClient.Subscribe(ctx, kafka.TopicTripCompleted, "driver-trip-completed", func(data []byte) error {
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

	kafkaClient.Subscribe(ctx, kafka.TopicRideGoEvents, "driver-ridego-events", func(data []byte) error {
		var env kafka.EventEnvelope
		if err := json.Unmarshal(data, &env); err != nil {
			return err
		}
		switch env.Type {
		case kafka.EventTypeTripCancelled:
			var ev kafka.TripCancelledEvent
			if err := json.Unmarshal(env.Payload, &ev); err != nil {
				return err
			}
			if ev.DriverID == "" {
				return nil
			}
			log.Printf("[driver] trip.cancelled: driver=%s → status=available", ev.DriverID)
			return repo.UpdateStatus(ctx, ev.DriverID, model.StatusAvailable)
		case kafka.EventTypeRatingSubmitted:
			var ev kafka.RatingSubmittedEvent
			if err := json.Unmarshal(env.Payload, &ev); err != nil {
				return err
			}
			if ev.RateeRole != model.RoleDriver {
				return nil
			}
			log.Printf("[driver] rating.submitted: driver=%s score=%d", ev.RateeID, ev.Score)
			return repo.UpdateRating(ctx, ev.RateeID, ev.Score)
		}
		return nil
	})

	h := controllers.New(svc)

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
