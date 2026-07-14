package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/robintheclawdotcom/robin-the-claw/runtime/sequencer-publisher/aaplrelay"
)

func main() {
	if err := run(); err != nil {
		slog.Error("AAPL relay stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	config, err := aaplrelay.LoadConfig()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	first, err := ethclient.DialContext(ctx, config.SourceRPC1)
	if err != nil {
		return errors.New("connect first Arbitrum RPC")
	}
	defer first.Close()
	second, err := ethclient.DialContext(ctx, config.SourceRPC2)
	if err != nil {
		return errors.New("connect second Arbitrum RPC")
	}
	defer second.Close()
	target, err := ethclient.DialContext(ctx, config.TargetRPC)
	if err != nil {
		return errors.New("connect Robinhood RPC")
	}
	defer target.Close()
	source, err := aaplrelay.NewSourceReader(first, second, config)
	if err != nil {
		return err
	}
	feed, err := aaplrelay.NewTargetFeed(config)
	if err != nil {
		return err
	}
	journal, err := aaplrelay.OpenJournal(ctx, config)
	if err != nil {
		return err
	}
	defer journal.Close(context.Background())
	metrics := new(aaplrelay.Metrics)
	service := aaplrelay.NewService(config, target, source, feed, journal, metrics)
	server := &http.Server{
		Addr: config.ListenAddress, Handler: aaplrelay.MetricsHandler(metrics, config.Interval),
		ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second,
	}
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("AAPL relay metrics server stopped", "error", err)
			stop()
		}
	}()
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	slog.Info(
		"AAPL relay started",
		"publisher", config.PublisherID,
		"signer", config.SignerAddress.Hex(),
		"source", config.SourceFeed.Hex(),
		"target", config.TargetFeed.Hex(),
	)
	return service.Run(ctx)
}
