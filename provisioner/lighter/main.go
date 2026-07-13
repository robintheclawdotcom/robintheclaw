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

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
)

func main() {
	if err := run(); err != nil {
		slog.Error("Lighter provisioner stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	value, err := loadConfig()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	state := &server{config: value, now: time.Now}
	var store *pgStore
	if value.Enabled {
		startup, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		store, err = openStore(startup, value.DatabaseURL)
		if err != nil {
			return err
		}
		defer store.Close()
		aws, err := awsconfig.LoadDefaultConfig(startup)
		if err != nil {
			return errors.New("load AWS configuration")
		}
		state.store = store
		state.service = &service{
			store:    store,
			envelope: newEnvelope(awskms.NewFromConfig(aws), value.KMSKeyID),
			lighter:  newLiveLighterClient(value.APIURL, value.ChainID),
			ttl:      value.AssociationTTL,
			now:      time.Now,
		}
		state.signingSlots = make(chan struct{}, value.SigningMaxConcurrent)
	}

	httpServer := &http.Server{
		Addr:              value.ListenAddress,
		Handler:           state.handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	result := make(chan error, 1)
	go func() { result <- httpServer.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdown)
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
