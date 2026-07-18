package main

import (
	"log"
	"net/http"
	"time"

	"github.com/robin-the-claw/liveexec/protocol"
	"github.com/robin-the-claw/liveexec/strategyrunner"
)

func main() {
	config, err := strategyrunner.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}
	if !config.Enabled {
		server := strategyrunner.NewServer(nil, nil, false)
		log.Printf("strategy runner disabled; listening on %s", config.ListenAddress)
		log.Fatal(listen(config.ListenAddress, server.Handler()))
	}
	auth, err := protocol.NewAuthenticator(config.AuthKey, config.Caller)
	if err != nil {
		log.Fatal(err)
	}
	coordinator, err := strategyrunner.NewCoordinatorClientWithExit(
		config.CoordinatorURL,
		config.CoordinatorCaller,
		config.CoordinatorKey,
		config.CoordinatorExitCaller,
		config.CoordinatorExitKey,
	)
	if err != nil {
		log.Fatal(err)
	}
	service, err := strategyrunner.NewService(config.QuotePublicKey, coordinator, config.LighterMarketIndex)
	if err != nil {
		log.Fatal(err)
	}
	server := strategyrunner.NewServer(service, auth, true)
	log.Printf("strategy runner listening on %s", config.ListenAddress)
	log.Fatal(listen(config.ListenAddress, server.Handler()))
}

func listen(address string, handler http.Handler) error {
	server := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	return server.ListenAndServe()
}
