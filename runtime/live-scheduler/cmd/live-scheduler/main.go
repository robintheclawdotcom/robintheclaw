package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	scheduler "github.com/robin-the-claw/live-scheduler"
)

func main() {
	config, err := scheduler.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if !config.Enabled {
		log.Print("live scheduler is disabled")
		<-ctx.Done()
		return
	}
	pool, err := pgxpool.New(ctx, config.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatal(err)
	}
	store, err := scheduler.NewPGStore(pool, config.WorkerID)
	if err != nil {
		log.Fatal(err)
	}
	client := &http.Client{Timeout: config.RequestTimeout}
	quotes, err := scheduler.NewQuoteClient(client, config.QuoteURL, config.QuoteCaller, config.QuoteKey)
	if err != nil {
		log.Fatal(err)
	}
	runner, err := scheduler.NewRunnerClient(client, config.RunnerURL, config.RunnerCaller, config.RunnerKey)
	if err != nil {
		log.Fatal(err)
	}
	service, err := scheduler.New(store, quotes, runner, config.QuotePublicKey, config.LighterMarket, config.LeaseDuration)
	if err != nil {
		log.Fatal(err)
	}
	for {
		err := service.RunOnce(ctx)
		if err != nil && !errors.Is(err, scheduler.ErrNoDispatch) {
			log.Printf("live scheduler poll failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(config.PollInterval):
		}
	}
}
