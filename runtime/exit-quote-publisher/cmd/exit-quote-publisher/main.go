package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	exitquote "github.com/robin-the-claw/exit-quote-publisher"
)

func main() {
	config, err := exitquote.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if !config.Enabled {
		log.Print("exit quote publisher is disabled")
		<-ctx.Done()
		return
	}
	poolConfig, err := pgxpool.ParseConfig(config.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	poolConfig.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, "SET default_transaction_read_only = on")
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatal(err)
	}
	store, err := exitquote.NewPGStore(pool)
	if err != nil {
		log.Fatal(err)
	}
	client := &http.Client{Timeout: config.RequestTimeout}
	quotes, err := exitquote.NewQuoteClient(client, config.QuoteURL, config.QuoteCaller, config.QuoteKey)
	if err != nil {
		log.Fatal(err)
	}
	publisher, err := exitquote.New(store, quotes, config.QuotePublicKey, config.LighterMarket)
	if err != nil {
		log.Fatal(err)
	}
	for {
		err := publisher.RunOnce(ctx)
		if err != nil && !errors.Is(err, exitquote.ErrNoCandidate) {
			log.Printf("exit quote publication failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(config.PollInterval):
		}
	}
}
