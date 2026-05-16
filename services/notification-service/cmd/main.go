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
			_ = m.Send(ev.RiderEmail, "Your RideGo trip is complete", tripCompletedEmailBody(ev.Fare))
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
				_ = m.Send(ev.RiderEmail, "Trip Cancelled — RideGo", tripCancelledEmailBody())
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
				_ = m.Send(ev.RiderEmail, "Payment confirmed — RideGo", paymentCompletedEmailBody(ev.Amount, ev.TripID))
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

func tripCancelledEmailBody() string {
	return buildEmailLayout(`
<h2 style="margin:0 0 12px;font-size:22px;font-weight:700;color:#0F172A;">Trip Cancelled</h2>
<p style="margin:0 0 24px;font-size:15px;color:#475569;line-height:1.7;">
  Your trip has been cancelled. No charge has been applied to your account.
</p>
<p style="margin:0;font-size:13px;color:#94A3B8;line-height:1.6;">
  If you did not cancel this trip or have any concerns, please reach out to our support team.
</p>`)
}

func tripCompletedEmailBody(fare float64) string {
	content := fmt.Sprintf(`
<h2 style="margin:0 0 12px;font-size:22px;font-weight:700;color:#0F172A;">Trip Completed</h2>
<p style="margin:0 0 24px;font-size:15px;color:#475569;line-height:1.7;">
  Your trip has been completed successfully. Here is a summary of your ride.
</p>
<table cellpadding="0" cellspacing="0" border="0" width="100%%" style="margin-bottom:24px;">
  <tr>
    <td style="background:#F8FAFC;border:1px solid #E2E8F0;border-radius:6px;padding:20px 24px;">
      <p style="margin:0 0 4px;font-size:11px;font-weight:600;color:#94A3B8;letter-spacing:1px;text-transform:uppercase;">Total Fare</p>
      <p style="margin:0;font-size:32px;font-weight:700;color:#0F172A;letter-spacing:-0.5px;">&#8377;%.2f</p>
    </td>
  </tr>
</table>
<p style="margin:0;font-size:13px;color:#94A3B8;line-height:1.6;">Thank you for riding with RideGo. We hope to see you again soon.</p>`, fare)
	return buildEmailLayout(content)
}

func paymentCompletedEmailBody(amount float64, tripID string) string {
	content := fmt.Sprintf(`
<h2 style="margin:0 0 12px;font-size:22px;font-weight:700;color:#0F172A;">Payment Confirmed</h2>
<p style="margin:0 0 24px;font-size:15px;color:#475569;line-height:1.7;">
  Your payment has been successfully processed.
</p>
<table cellpadding="0" cellspacing="0" border="0" width="100%%" style="margin-bottom:24px;">
  <tr>
    <td style="background:#F8FAFC;border:1px solid #E2E8F0;border-radius:6px;padding:20px 24px;">
      <p style="margin:0 0 4px;font-size:11px;font-weight:600;color:#94A3B8;letter-spacing:1px;text-transform:uppercase;">Amount Paid</p>
      <p style="margin:0;font-size:32px;font-weight:700;color:#0F172A;letter-spacing:-0.5px;">&#8377;%.2f</p>
      <p style="margin:8px 0 0;font-size:12px;color:#94A3B8;">Trip ID: %s</p>
    </td>
  </tr>
</table>
<p style="margin:0;font-size:13px;color:#94A3B8;line-height:1.6;">Thank you for using RideGo.</p>`, amount, tripID)
	return buildEmailLayout(content)
}

func buildEmailLayout(content string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8"/>
  <meta name="viewport" content="width=device-width,initial-scale=1.0"/>
  <title>RideGo</title>
</head>
<body style="margin:0;padding:0;background-color:#F1F5F9;font-family:'Helvetica Neue',Helvetica,Arial,sans-serif;">
  <table width="100%%" cellpadding="0" cellspacing="0" border="0" style="background-color:#F1F5F9;padding:48px 0;">
    <tr><td align="center">
      <table width="560" cellpadding="0" cellspacing="0" border="0"
             style="background-color:#ffffff;border-radius:8px;overflow:hidden;box-shadow:0 4px 24px rgba(0,0,0,0.07);">
        <tr>
          <td style="background-color:#1A1A2E;padding:32px 48px;">
            <span style="color:#ffffff;font-size:22px;font-weight:700;letter-spacing:1px;">RIDEGO</span>
            <p style="margin:6px 0 0;color:rgba(255,255,255,0.45);font-size:11px;letter-spacing:2px;text-transform:uppercase;">On-demand rides</p>
          </td>
        </tr>
        <tr><td style="padding:48px 48px 36px;">%s</td></tr>
        <tr><td style="padding:0 48px;"><hr style="border:none;border-top:1px solid #E2E8F0;margin:0;"/></td></tr>
        <tr><td style="padding:28px 48px 36px;">
          <p style="margin:0 0 4px;font-size:12px;color:#94A3B8;line-height:1.7;">
            This is an automated message from RideGo. Please do not reply to this email.
          </p>
          <p style="margin:12px 0 0;font-size:12px;color:#CBD5E1;line-height:1.6;text-align:center;">
            Created with &#10084;&#65039; by Mulukutla Sai Krishna
          </p>
        </td></tr>
      </table>
    </td></tr>
  </table>
</body>
</html>`, content)
}
