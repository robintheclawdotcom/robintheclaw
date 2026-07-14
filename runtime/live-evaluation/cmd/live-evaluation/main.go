package main

import (
	"context"
	"errors"
	"log"
	"os/signal"
	"syscall"
	"time"

	evaluation "github.com/robin-the-claw/live-evaluation"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	config, err := evaluation.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}
	if !config.Enabled {
		<-ctx.Done()
		return
	}
	researchPool, err := evaluation.NewReadOnlyPool(ctx, config.ResearchDatabase)
	if err != nil {
		log.Fatal(err)
	}
	defer researchPool.Close()
	productPool, err := evaluation.NewReadOnlyPool(ctx, config.ProductDatabase)
	if err != nil {
		log.Fatal(err)
	}
	defer productPool.Close()
	executionPool, err := evaluation.NewWritePool(ctx, config.ExecutionDatabase)
	if err != nil {
		log.Fatal(err)
	}
	defer executionPool.Close()
	research, err := evaluation.NewPGResearchSource(researchPool)
	if err != nil {
		log.Fatal(err)
	}
	product, err := evaluation.NewPGProductSource(productPool)
	if err != nil {
		log.Fatal(err)
	}
	store, err := evaluation.NewPGStore(executionPool)
	if err != nil {
		log.Fatal(err)
	}
	if err := store.Ready(ctx); err != nil {
		log.Fatal(err)
	}
	market, inserted, err := evaluation.BootstrapMarketConfig(ctx,
		evaluation.NewLighterMarketSource(nil, nil), store, config.MarketBootstrap, time.Now().UTC())
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("verified Lighter AAPL market manifest %s (inserted=%t)", market.ManifestID, inserted)
	service, err := evaluation.NewService(research, product, store, config)
	if err != nil {
		log.Fatal(err)
	}
	service.SetErrorHandler(func(err error) { log.Printf("live evaluation rejected evidence: %v", err) })
	if err := service.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}
