package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
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
	rredis "uber/shared/pkg/redis"
	"uber/trip-service/config"
	"uber/trip-service/internal/controllers"
	"uber/trip-service/internal/tracking"
	"uber/trip-service/internal/repositories"
	"uber/trip-service/internal/service"
	"uber/trip-service/migrations"
)

func generateRideOTP() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(10_000))
	if err != nil {
		return "", fmt.Errorf("generateRideOTP: %w", err)
	}
	return fmt.Sprintf("%04d", n.Int64()), nil
}

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
		kafka.TopicRideGoEvents,
	); err != nil {
		log.Fatal(err)
	}
	kafkaClient.WarmWriters(
		kafka.TopicRideRequested,
		kafka.TopicTripCompleted,
		kafka.TopicRideGoEvents,
	)

	repo := repositories.NewRepository(pool)
	svc := service.New(repo, kafkaClient, redisClient)

	// Kafka consumers
	kafkaClient.Subscribe(ctx, kafka.TopicDriverAssigned, "trip-driver-assigned", func(data []byte) error {
		var ev kafka.DriverAssignedEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		log.Printf("[trip] driver.assigned: trip=%s driver=%s", ev.TripID, ev.DriverID)
		if err := repo.AssignDriver(ctx, ev.TripID, ev.DriverID); err != nil {
			return err
		}
		otp, err := generateRideOTP()
		if err != nil {
			log.Printf("[trip] warn: failed to generate OTP for trip %s: %v", ev.TripID, err)
			return nil // non-fatal: trip is assigned, OTP failure shouldn't block
		}
		if err := redisClient.SetTripOTP(ctx, ev.TripID, otp); err != nil {
			log.Printf("[trip] warn: failed to store OTP for trip %s: %v", ev.TripID, err)
		}
		return nil
	})

	// Background poller: cancel trips stuck in REQUESTED > 5 minutes
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				stuck, err := repo.FindStuckTrips(ctx, 10*time.Minute)
				if err != nil {
					log.Printf("[trip-poller] error fetching stuck trips: %v", err)
					continue
				}
				for _, t := range stuck {
					now := time.Now()
					if err := repo.CancelTrip(ctx, t.ID, now); err != nil {
						log.Printf("[trip-poller] failed to cancel trip %s: %v", t.ID, err)
						continue
					}
					ev := kafka.TripCancelledEvent{
						TripID:      t.ID,
						RiderID:     t.RiderID,
						RiderEmail:  t.RiderEmail,
						Reason:      "no_driver_available",
						CancelledAt: now.Format(time.RFC3339),
					}
					if env, envErr := kafka.NewEnvelope(kafka.EventTypeTripCancelled, ev); envErr == nil {
						if err := kafkaClient.Publish(ctx, kafka.TopicRideGoEvents, t.ID, env); err != nil {
							log.Printf("[trip-poller] failed to publish trip.cancelled for %s: %v", t.ID, err)
						}
					}
					log.Printf("[trip-poller] cancelled stuck trip %s (no driver after 5 min)", t.ID)
				}
			}
		}
	}()

	wsHub := tracking.NewHub(cfg.AllowedOrigins)
	h := controllers.New(svc, wsHub, cfg.InternalSecret)

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
