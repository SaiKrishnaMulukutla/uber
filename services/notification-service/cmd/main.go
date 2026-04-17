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
		kafka.TopicTripCancelled,
		kafka.TopicRatingSubmitted,
		kafka.TopicPaymentCompleted,
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
		return repo.Create(ctx, ev.DriverID, "driver_assigned", "New Trip Assigned", fmt.Sprintf("You have been assigned to trip %s.", ev.TripID), ev.TripID+":"+ev.DriverID+":driver_assigned")
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
			_ = m.Send(ev.RiderEmail, "Your Uber trip is complete", tripCompletedEmailBody(ev.Fare))
		}
		if ev.DriverID != "" {
			return repo.Create(ctx, ev.DriverID, "trip_completed", "Trip Completed", fmt.Sprintf("Trip completed. Earnings: ₹%.2f", ev.Fare), ev.TripID+":"+ev.DriverID+":trip_completed")
		}
		return nil
	})

	kafkaClient.Subscribe(ctx, kafka.TopicTripCancelled, "notif-trip-cancelled", func(data []byte) error {
		var ev kafka.TripCancelledEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		log.Printf("[notifications] trip.cancelled: trip=%s", ev.TripID)
		if err := repo.Create(ctx, ev.RiderID, "trip_cancelled", "Trip Cancelled", "Your trip has been cancelled.", ev.TripID+":"+ev.RiderID+":trip_cancelled"); err != nil {
			return err
		}
		if m != nil && ev.RiderEmail != "" {
			_ = m.Send(ev.RiderEmail, "Trip Cancelled — Uber", tripCancelledEmailBody())
		}
		if ev.DriverID != "" {
			return repo.Create(ctx, ev.DriverID, "trip_cancelled", "Trip Cancelled", "The trip has been cancelled.", ev.TripID+":"+ev.DriverID+":trip_cancelled")
		}
		return nil
	})

	kafkaClient.Subscribe(ctx, kafka.TopicRatingSubmitted, "notif-rating-submitted", func(data []byte) error {
		var ev kafka.RatingSubmittedEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		if ev.RateeID == "" {
			log.Printf("[notifications] rating.submitted: skipping — empty ratee_id for trip %s", ev.TripID)
			return nil
		}
		log.Printf("[notifications] rating.submitted: ratee=%s score=%d", ev.RateeID, ev.Score)
		return repo.Create(ctx, ev.RateeID, "rating_received", "New Rating", fmt.Sprintf("You received a %d-star rating.", ev.Score), ev.TripID+":"+ev.RateeID+":rating_received")
	})

	kafkaClient.Subscribe(ctx, kafka.TopicPaymentCompleted, "notif-payment-completed", func(data []byte) error {
		var ev kafka.PaymentCompletedEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		log.Printf("[notifications] payment.completed: trip=%s amount=%.2f", ev.TripID, ev.Amount)
		if err := repo.Create(ctx, ev.RiderID, "payment_completed", "Payment Processed", fmt.Sprintf("Payment of ₹%.2f has been processed for your trip.", ev.Amount), ev.TripID+":"+ev.RiderID+":payment_completed"); err != nil {
			return err
		}
		if m != nil && ev.RiderEmail != "" {
			_ = m.Send(ev.RiderEmail, "Payment confirmed — Uber", paymentCompletedEmailBody(ev.Amount, ev.TripID))
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

func tripCancelledEmailBody() string {
	return buildEmailLayout(`
<p style="margin:0 0 8px;font-size:22px;font-weight:700;color:#000000;">Trip Cancelled</p>
<p style="margin:0 0 20px;font-size:15px;color:#545454;line-height:1.6;">
  Your trip has been cancelled. We hope to see you again soon.
</p>
<p style="margin:0;font-size:13px;color:#888888;">If you didn't cancel this trip, please contact support.</p>`)
}

func tripCompletedEmailBody(fare float64) string {
	content := fmt.Sprintf(`
<p style="margin:0 0 8px;font-size:22px;font-weight:700;color:#000000;">Trip Completed</p>
<p style="margin:0 0 20px;font-size:15px;color:#545454;line-height:1.6;">
  Your trip has been completed successfully.
</p>
<table cellpadding="0" cellspacing="0" border="0" style="margin-bottom:20px;">
  <tr><td style="background-color:#f6f6f6;border-radius:4px;padding:16px 28px;">
    <span style="font-size:20px;font-weight:700;color:#000000;">Fare charged: ₹%.2f</span>
  </td></tr>
</table>
<p style="margin:0;font-size:13px;color:#888888;">Thank you for riding with Uber.</p>`, fare)
	return buildEmailLayout(content)
}

func paymentCompletedEmailBody(amount float64, tripID string) string {
	content := fmt.Sprintf(`
<p style="margin:0 0 8px;font-size:22px;font-weight:700;color:#000000;">Payment Confirmed</p>
<p style="margin:0 0 20px;font-size:15px;color:#545454;line-height:1.6;">
  Your payment has been processed successfully.
</p>
<table cellpadding="0" cellspacing="0" border="0" style="margin-bottom:20px;">
  <tr><td style="background-color:#f6f6f6;border-radius:4px;padding:16px 28px;">
    <span style="font-size:20px;font-weight:700;color:#000000;">₹%.2f</span>
    <span style="font-size:13px;color:#888888;margin-left:8px;">Trip %s</span>
  </td></tr>
</table>
<p style="margin:0;font-size:13px;color:#888888;">Thank you for riding with Uber.</p>`, amount, tripID)
	return buildEmailLayout(content)
}

func buildEmailLayout(content string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head><meta charset="UTF-8"/><title>Uber</title></head>
<body style="margin:0;padding:0;background-color:#f6f6f6;font-family:'Helvetica Neue',Helvetica,Arial,sans-serif;">
  <table width="100%%" cellpadding="0" cellspacing="0" border="0" style="background-color:#f6f6f6;padding:40px 0;">
    <tr><td align="center">
      <table width="520" cellpadding="0" cellspacing="0" border="0"
             style="background-color:#ffffff;border-radius:4px;overflow:hidden;box-shadow:0 2px 8px rgba(0,0,0,0.08);">
        <tr><td style="background-color:#000000;padding:28px 40px;">
          <span style="color:#ffffff;font-size:26px;font-weight:700;letter-spacing:-0.5px;">Uber</span>
        </td></tr>
        <tr><td style="padding:40px 40px 28px;">%s</td></tr>
        <tr><td style="padding:0 40px;"><hr style="border:none;border-top:1px solid #eeeeee;margin:0;"/></td></tr>
        <tr><td style="padding:24px 40px 32px;">
          <p style="margin:0 0 4px;font-size:12px;color:#aaaaaa;line-height:1.6;">
            This is an automated message from Uber. Please do not reply to this email.
          </p>
          <p style="margin:8px 0 0;font-size:12px;color:#555555;line-height:1.6;text-align:center;">
            Created with &#10084;&#65039; by Mulukutla Sai Krishna
          </p>
        </td></tr>
      </table>
    </td></tr>
  </table>
</body>
</html>`, content)
}
