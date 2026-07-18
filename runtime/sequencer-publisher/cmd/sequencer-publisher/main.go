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
	sequencerpublisher "github.com/robintheclawdotcom/robin-the-claw/runtime/sequencer-publisher"
)

func main() {
	if err := run(); err != nil {
		slog.Error("sequencer publisher stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	config, err := sequencerpublisher.LoadConfig()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	dialContext, cancel := context.WithTimeout(ctx, config.RequestTimeout)
	source, err := ethclient.DialContext(dialContext, config.SourceRPCURL)
	if err != nil {
		cancel()
		return errors.New("connect sequencer source RPC")
	}
	defer source.Close()
	transaction, err := ethclient.DialContext(dialContext, config.TransactionRPCURL)
	cancel()
	if err != nil {
		return errors.New("connect sequencer transaction RPC")
	}
	defer transaction.Close()
	journalContext, journalCancel := context.WithTimeout(ctx, config.RequestTimeout)
	journal, err := sequencerpublisher.OpenJournal(
		journalContext, config.DatabaseURL, config.PublisherID, config.FeedAddress, config.SignerAddress,
		config.RunMigrations,
	)
	journalCancel()
	if err != nil {
		return err
	}
	defer journal.Close(context.Background())
	feed, err := sequencerpublisher.NewFeed(config.FeedAddress, config.FeedCodeHash)
	if err != nil {
		return err
	}
	metrics := new(sequencerpublisher.Metrics)
	service := sequencerpublisher.NewService(config, source, transaction, feed, journal, metrics)
	server := &http.Server{
		Addr: config.ListenAddress, Handler: sequencerpublisher.MetricsHandler(metrics, config.Interval),
		ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second,
		IdleTimeout:    30 * time.Second,
		MaxHeaderBytes: 16 << 10,
	}
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("sequencer publisher metrics server stopped", "error", err)
			stop()
		}
	}()
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	slog.Info("sequencer publisher started", "publisher", config.PublisherID, "signer", config.SignerAddress.Hex())
	return service.Run(ctx)
}
