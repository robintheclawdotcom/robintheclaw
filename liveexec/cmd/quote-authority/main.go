package main

import (
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/robin-the-claw/liveexec/quoteauthority"
)

func main() {
	config, err := quoteauthority.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}
	if config.Enabled {
		log.Fatal(errors.New("no reviewed live executable-quote adapter is compiled"))
	}
	server := quoteauthority.NewServer(nil, nil, false)
	log.Printf("quote authority disabled; listening on %s", config.ListenAddress)
	httpServer := &http.Server{
		Addr:              config.ListenAddress,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Fatal(httpServer.ListenAndServe())
}
