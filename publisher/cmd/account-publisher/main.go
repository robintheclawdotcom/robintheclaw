package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	publisher "github.com/robin-the-claw/publisher"
)

func main() {
	config, err := publisher.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}
	service, err := publisher.NewService(config, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer service.Close()
	server := &http.Server{
		Addr: config.ListenAddress, Handler: service.HealthHandler(),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second,
		MaxHeaderBytes: 16 << 10,
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Print("health server stopped")
			cancel()
		}
	}()
	go func() {
		if err := service.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Print("publisher stopped")
			cancel()
		}
	}()
	<-ctx.Done()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		os.Exit(1)
	}
}
