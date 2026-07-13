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
		slog.Error("robinhood signer stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	config, err := loadConfig()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serverState := &Server{config: config, writers: make(map[string]*Writer)}
	var journals []*Journal
	if config.Enabled {
		startup, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		rpcClient, err := rpc.DialContext(startup, config.RPCURL)
		if err != nil {
			return errors.New("connect Robinhood RPC")
		}
		defer rpcClient.Close()
		client := ethclient.NewClient(rpcClient)
		reconciliationRPC, err := rpc.DialContext(startup, config.ReconciliationRPCURL)
		if err != nil {
			return errors.New("connect reconciliation RPC")
		}
		defer reconciliationRPC.Close()
		verifier := ethclient.NewClient(reconciliationRPC)
		aws, err := awsconfig.LoadDefaultConfig(startup)
		if err != nil {
			return errors.New("load AWS configuration")
		}
		accounts, err := config.accountConfigs()
		if err != nil {
			return err
		}
		kmsClient := awskms.NewFromConfig(aws)
		for _, account := range accounts {
			signer, err := newKMSSigner(startup, kmsClient, account.KMSKeyID)
			if err != nil {
				return err
			}
			manifest, deploymentID := account.manifest()
			journal, err := openJournal(startup, account.DatabaseURL, manifest, deploymentID)
			if err != nil {
				return err
			}
			journals = append(journals, journal)
			writer := newWriter(account, client, verifier, signer, journal)
			if err := writer.Recover(startup); err != nil {
				return err
			}
			serverState.writers[account.ExecutionAccountID] = writer
			go writer.RunReconciler(ctx)
		}
		defer func() {
			for _, journal := range journals {
				journal.Close()
			}
		}()
	}

	server := httpServer(config, serverState.Handler())
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
