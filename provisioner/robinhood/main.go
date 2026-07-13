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
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

func main() {
	if err := run(); err != nil {
		slog.Error("Robinhood provisioner stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	configuration, err := loadConfig()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	state := &server{config: configuration, now: time.Now}
	var store *pgStore
	var primaryRPC, secondaryRPC *rpc.Client
	if configuration.Enabled {
		startup, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		store, err = openStore(startup, configuration.DatabaseURL)
		if err != nil {
			return err
		}
		defer store.Close()
		primaryRPC, err = rpc.DialContext(startup, configuration.PrimaryRPCURL)
		if err != nil {
			return errors.New("connect primary Robinhood RPC")
		}
		defer primaryRPC.Close()
		secondaryRPC, err = rpc.DialContext(startup, configuration.SecondaryRPCURL)
		if err != nil {
			return errors.New("connect secondary Robinhood RPC")
		}
		defer secondaryRPC.Close()
		aws, err := awsconfig.LoadDefaultConfig(startup)
		if err != nil {
			return errors.New("load AWS configuration")
		}
		chain := &chainVerifier{
			config:    configuration,
			primary:   ethclient.NewClient(primaryRPC),
			secondary: ethclient.NewClient(secondaryRPC),
		}
		state.store = store
		state.service = &service{
			config: configuration,
			store:  store,
			keys:   &keyProvisioner{client: awskms.NewFromConfig(aws), aliasPrefix: configuration.KMSAliasPrefix},
			chain:  chain,
		}
	}
	server := &http.Server{
		Addr:              configuration.ListenAddress,
		Handler:           state.handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	result := make(chan error, 1)
	go func() { result <- server.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
