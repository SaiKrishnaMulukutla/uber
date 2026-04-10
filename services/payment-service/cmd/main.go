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

	"uber/payment-service/config"
	"uber/payment-service/internal/controllers"
	"uber/payment-service/internal/hub"
	"uber/payment-service/internal/provider"
	cashprov "uber/payment-service/internal/provider/cash"
	rzpprov "uber/payment-service/internal/provider/razorpay"
	"uber/payment-service/internal/repositories"
	"uber/payment-service/internal/service"
	"uber/payment-service/migrations"
	"uber/shared/pkg/db"
	"uber/shared/pkg/jwt"
	"uber/shared/pkg/kafka"
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
	if err := kafkaClient.EnsureTopics(ctx,
		kafka.TopicTripCompleted,
		kafka.TopicPaymentCompleted,
	); err != nil {
		log.Fatal(err)
	}

	// Select payment provider based on config
	var prov provider.PaymentProvider
	switch cfg.PaymentProvider {
	case "razorpay":
		rzp := rzpprov.New(cfg.RazorpayKeyID, cfg.RazorpayKeySecret, cfg.RazorpayWebhookSecret, cfg.TLSSkipVerify)
		// Register webhook on startup (non-fatal if it fails)
		webhookURL := cfg.BaseURL + "/payments/webhook"
		if err := rzp.RegisterWebhook(ctx, webhookURL); err != nil {
			log.Printf("[payments] webhook registration failed (non-fatal): %v", err)
		}
		prov = rzp
		log.Println("[payments] using Razorpay payment provider")
	default:
		prov = cashprov.New()
		log.Println("[payments] using cash payment provider")
	}

	paymentHub := hub.New()

	repo := repositories.NewRepository(pool)
	svc := service.NewService(repo, kafkaClient, prov, cfg.RazorpayKeyID, paymentHub, cfg.BaseURL)

	// Kafka consumer: trip.completed → create payment
	kafkaClient.Subscribe(ctx, kafka.TopicTripCompleted, "payment-trip-completed", func(data []byte) error {
		var ev kafka.TripCompletedEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		log.Printf("[payments] trip.completed: trip=%s fare=%.2f method=%s", ev.TripID, ev.Fare, ev.PaymentMethod)
		_, err := svc.InitPayment(ctx, ev.TripID, ev.RiderID, ev.RiderEmail, ev.RiderPhone, ev.DriverID, ev.PaymentMethod, ev.Fare)
		return err
	})

	h := controllers.NewHandler(svc, paymentHub, cfg.RazorpayKeyID)

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
			w.Write([]byte(`{"status":"unhealthy","service":"payment-service"}`))
			return
		}
		w.Write([]byte(`{"status":"ok","service":"payment-service"}`))
	})
	r.Mount("/payments", h.Routes())

	srv := &http.Server{Addr: ":" + cfg.Port, Handler: r}
	go func() {
		log.Printf("payment-service listening on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down payment-service...")

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	srv.Shutdown(shutCtx)
	cancel()
}
