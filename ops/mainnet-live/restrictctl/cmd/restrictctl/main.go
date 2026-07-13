package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	restrictctl "github.com/robin-the-claw/restrictctl"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "restrictctl: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	flags := flag.NewFlagSet("restrictctl", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	requestID := flags.String("request-id", "", "stable idempotency identity")
	scope := flags.String("scope", "", "global, strategy, or account")
	strategyVersion := flags.String("strategy-version", "", "strategy identity for strategy or account scope")
	executionAccountID := flags.String("execution-account-id", "", "execution account identity for account scope")
	expectedVersion := flags.String("expected-version", "", "current control version")
	fromMode := flags.String("from", "", "expected current mode")
	targetMode := flags.String("to", "", "REDUCE_ONLY or HALTED")
	reason := flags.String("reason", "", "operator restriction reason")
	evidenceSHA256 := flags.String("evidence-sha256", "", "restriction evidence digest")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	version, err := strconv.ParseInt(*expectedVersion, 10, 64)
	if err != nil {
		return errors.New("expected version must be a non-negative integer")
	}
	request := restrictctl.Request{
		RequestID:          *requestID,
		Scope:              restrictctl.Scope(*scope),
		StrategyVersion:    *strategyVersion,
		ExecutionAccountID: *executionAccountID,
		ExpectedVersion:    version,
		FromMode:           restrictctl.Mode(*fromMode),
		TargetMode:         restrictctl.Mode(*targetMode),
		Reason:             *reason,
		EvidenceSHA256:     *evidenceSHA256,
		OperatorID:         os.Getenv("RESTRICTCTL_OPERATOR_ID"),
	}
	if err := restrictctl.Validate(request); err != nil {
		return err
	}
	databaseURL := os.Getenv("RESTRICTCTL_DATABASE_URL")
	privateKeyPath := os.Getenv("RESTRICTCTL_PRIVATE_KEY_FILE")
	publicKeyPath := os.Getenv("RESTRICTCTL_PUBLIC_KEY_FILE")
	if databaseURL == "" || privateKeyPath == "" || publicKeyPath == "" {
		return errors.New("RESTRICTCTL_DATABASE_URL and signing key file variables are required")
	}
	privateKey, publicKey, err := restrictctl.LoadKeyPair(privateKeyPath, publicKeyPath)
	if err != nil {
		return err
	}
	signed, err := restrictctl.Sign(request, privateKey, publicKey)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return errors.New("database configuration is invalid")
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return errors.New("database is unavailable")
	}
	store, err := restrictctl.NewPGStore(pool)
	if err != nil {
		return err
	}
	result, err := store.Apply(ctx, signed)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(result)
}
