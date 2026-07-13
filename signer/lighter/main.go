package main

import (
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	value, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	signer, err := newSignerServer(value)
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{
		Addr:              value.listenAddress,
		Handler:           signer.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		_ = server.Close()
	}()

	log.Printf("lighter signer listening on %s enabled=%t", value.listenAddress, value.enabled)
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
