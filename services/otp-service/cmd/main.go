package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"uber/otp-service/config"
	"uber/otp-service/internal/handler"
	"uber/otp-service/internal/mailer"
	"uber/otp-service/internal/repository"
	"uber/otp-service/internal/service"
)

func main() {
	cfg := config.Load()

	if cfg.EmailUser == "" || cfg.EmailPass == "" {
		log.Println("[otp-service] warn: EMAIL_USER/PASS not set — OTPs will be stored but not emailed")
	}

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		log.Fatalf("redis: cannot connect to %s: %v", cfg.RedisAddr, err)
	}
	defer rdb.Close()

	repo := repository.New(rdb)
	m := mailer.NewAsync(mailer.New(cfg), 10)
	svc := service.New(repo, m)
	h := handler.New(svc)

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: handler.SetupRouter(h, func() error {
			return rdb.Ping(context.Background()).Err()
		}),
	}

	go func() {
		log.Printf("otp-service listening on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down otp-service...")

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	srv.Shutdown(shutCtx)
}
