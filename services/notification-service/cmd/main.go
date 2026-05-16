package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	chimw "github.com/go-chi/chi/v5/middleware"

	"uber/notification-service/config"
	"uber/notification-service/internal/controllers"
	"uber/notification-service/internal/repositories"
	"uber/notification-service/migrations"
	"uber/shared/pkg/db"
	"uber/shared/pkg/jwt"
	"uber/shared/pkg/kafka"
	"uber/shared/pkg/mailer"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := config.Load()
	if err := jwt.Init(cfg.JWTSecret); err != nil {
		log.Fatal(err)
	}

	var m mailer.Mailer
	if cfg.EmailUser != "" && cfg.EmailPass != "" {
		smtp := mailer.New(cfg.EmailHost, cfg.EmailPort, cfg.EmailUser, cfg.EmailPass)
		if cfg.BrevoAPIKey != "" {
			m = mailer.NewAsync(mailer.WithFallback(mailer.NewBrevo(cfg.BrevoAPIKey, cfg.EmailUser), smtp), 5)
		} else {
			m = mailer.NewAsync(smtp, 5)
		}
	}

	pool := db.MustConnect(ctx, cfg.DatabaseURL, migrations.FS)

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

	// Kafka consumers
	kafkaClient.Subscribe(ctx, kafka.TopicRideOffered, "notif-ride-offered", func(data []byte) error {
		var ev kafka.RideOfferedEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		log.Printf("[notifications] ride.offered: trip=%s driver=%s", ev.TripID, ev.DriverID)
		return repo.Create(ctx, ev.DriverID, "ride_offered", "New Ride Request",
			"A rider needs a trip. Open the app to accept or decline.",
			ev.TripID+":"+ev.DriverID+":ride_offered")
	})

	kafkaClient.Subscribe(ctx, kafka.TopicRideRequested, "notif-ride-requested", func(data []byte) error {
		var ev kafka.RideRequestedEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		log.Printf("[notifications] ride.requested: trip=%s rider=%s", ev.TripID, ev.RiderID)
		return repo.Create(ctx, ev.RiderID, "ride_requested", "Ride Requested", "Your ride is being matched with a driver.", ev.TripID+":"+ev.RiderID+":ride_requested")
	})

	kafkaClient.Subscribe(ctx, kafka.TopicDriverAssigned, "notif-driver-assigned", func(data []byte) error {
		var ev kafka.DriverAssignedEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		log.Printf("[notifications] driver.assigned: trip=%s driver=%s", ev.TripID, ev.DriverID)
		if err := repo.Create(ctx, ev.DriverID, "driver_assigned", "New Trip Assigned",
			fmt.Sprintf("You have been assigned to trip %s.", ev.TripID),
			ev.TripID+":"+ev.DriverID+":driver_assigned"); err != nil {
			return err
		}
		if ev.RiderID != "" {
			return repo.Create(ctx, ev.RiderID, "driver_assigned", "Driver On the Way!",
				"Your driver has been assigned. Open the app to see your ride OTP.",
				ev.TripID+":"+ev.RiderID+":driver_assigned")
		}
		return nil
	})

	kafkaClient.Subscribe(ctx, kafka.TopicTripCompleted, "notif-trip-completed", func(data []byte) error {
		var ev kafka.TripCompletedEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		log.Printf("[notifications] trip.completed: trip=%s fare=%.2f", ev.TripID, ev.Fare)
		if err := repo.Create(ctx, ev.RiderID, "trip_completed", "Trip Completed", fmt.Sprintf("Your trip is complete. Fare: ₹%.2f", ev.Fare), ev.TripID+":"+ev.RiderID+":trip_completed"); err != nil {
			return err
		}
		if m != nil && ev.RiderEmail != "" {
			_ = m.Send(ev.RiderEmail, "Your RideGo trip is complete", mailer.TripCompleted(ev.Fare))
		}
		if ev.DriverID != "" {
			return repo.Create(ctx, ev.DriverID, "trip_completed", "Trip Completed", fmt.Sprintf("Trip completed. Earnings: ₹%.2f", ev.Fare), ev.TripID+":"+ev.DriverID+":trip_completed")
		}
		return nil
	})

	kafkaClient.Subscribe(ctx, kafka.TopicRideGoEvents, "notif-ridego-events", func(data []byte) error {
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
			log.Printf("[notifications] trip.cancelled: trip=%s", ev.TripID)
			if err := repo.Create(ctx, ev.RiderID, "trip_cancelled", "Trip Cancelled", "Your trip has been cancelled.", ev.TripID+":"+ev.RiderID+":trip_cancelled"); err != nil {
				return err
			}
			if m != nil && ev.RiderEmail != "" {
				_ = m.Send(ev.RiderEmail, "Trip Cancelled — RideGo", mailer.TripCancelled())
			}
			if ev.DriverID != "" {
				return repo.Create(ctx, ev.DriverID, "trip_cancelled", "Trip Cancelled", "The trip has been cancelled.", ev.TripID+":"+ev.DriverID+":trip_cancelled")
			}
		case kafka.EventTypeRatingSubmitted:
			var ev kafka.RatingSubmittedEvent
			if err := json.Unmarshal(env.Payload, &ev); err != nil {
				return err
			}
			if ev.RateeID == "" {
				log.Printf("[notifications] rating.submitted: skipping — empty ratee_id for trip %s", ev.TripID)
				return nil
			}
			log.Printf("[notifications] rating.submitted: ratee=%s score=%d", ev.RateeID, ev.Score)
			return repo.Create(ctx, ev.RateeID, "rating_received", "New Rating", fmt.Sprintf("You received a %d-star rating.", ev.Score), ev.TripID+":"+ev.RateeID+":rating_received")
		case kafka.EventTypePaymentCompleted:
			var ev kafka.PaymentCompletedEvent
			if err := json.Unmarshal(env.Payload, &ev); err != nil {
				return err
			}
			log.Printf("[notifications] payment.completed: trip=%s amount=%.2f", ev.TripID, ev.Amount)
			if err := repo.Create(ctx, ev.RiderID, "payment_completed", "Payment Processed", fmt.Sprintf("Payment of ₹%.2f has been processed for your trip.", ev.Amount), ev.TripID+":"+ev.RiderID+":payment_completed"); err != nil {
				return err
			}
			if m != nil && ev.RiderEmail != "" {
				_ = m.Send(ev.RiderEmail, "Payment confirmed — RideGo", mailer.PaymentCompleted(ev.Amount, ev.TripID))
			}
		}
		return nil
	})

	h := controllers.NewHandler(repo)

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
			w.Write([]byte(`{"status":"unhealthy","service":"notification-service"}`))
			return
		}
		w.Write([]byte(`{"status":"ok","service":"notification-service"}`))
	})
	r.Mount("/notifications", h.Routes())

	srv := &http.Server{Addr: ":" + cfg.Port, Handler: r}
	go func() {
		log.Printf("notification-service listening on :%s", cfg.Port)
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

