package main

import (
	"log"
	"net/http"
	"time"

	"github.com/robin-the-claw/liveexec/protocol"
	"github.com/robin-the-claw/liveexec/quoteauthority"
)

func main() {
	config, err := quoteauthority.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}
	if !config.Enabled {
		server := quoteauthority.NewServer(nil, nil, false)
		log.Printf("quote authority disabled; listening on %s", config.ListenAddress)
		log.Fatal(listen(config.ListenAddress, server.Handler()))
	}
	entryAuth, err := protocol.NewAuthenticator(config.AuthKey, config.Caller)
	if err != nil {
		log.Fatal(err)
	}
	exitAuth, err := protocol.NewAuthenticator(config.ExitAuthKey, config.ExitCaller)
	if err != nil {
		log.Fatal(err)
	}
	publisher, err := quoteauthority.NewCoordinatorPublisher(
		config.CoordinatorURL, config.CoordinatorCaller, config.CoordinatorKey,
	)
	if err != nil {
		log.Fatal(err)
	}
	resolver, err := quoteauthority.NewCoordinatorEpisodeResolver(
		config.CoordinatorURL, config.CoordinatorEpisodeCaller, config.CoordinatorEpisodeKey,
	)
	if err != nil {
		log.Fatal(err)
	}
	adapter, err := quoteauthority.NewLiveAdapter(config.Adapter, resolver)
	if err != nil {
		log.Fatal(err)
	}
	service, err := quoteauthority.NewService(adapter, publisher, config.QuoteSigningKey, config.LighterMarketIndex)
	if err != nil {
		log.Fatal(err)
	}
	server := quoteauthority.NewDualAuthServer(service, entryAuth, exitAuth, true)
	log.Printf("quote authority listening on %s", config.ListenAddress)
	log.Fatal(listen(config.ListenAddress, server.Handler()))
}

func listen(address string, handler http.Handler) error {
	server := &http.Server{
		Addr: address, Handler: handler,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second,
		MaxHeaderBytes: 16 << 10,
	}
	return server.ListenAndServe()
}
