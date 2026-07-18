package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestHelperProcess(t *testing.T) {
	if os.Getenv("ROBIN_LIVE_CONTROL_HELPER") != "1" {
		return
	}
	switch os.Getenv("ROBIN_LIVE_CONTROL_MODE") {
	case "exit":
		os.Exit(0)
	case "fail":
		os.Exit(23)
	case "block":
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
		<-signals
		os.Exit(0)
	default:
		os.Exit(2)
	}
}

func TestRunRejectsUnexpectedCleanExit(t *testing.T) {
	err := run(context.Background(), []commandSpec{
		helper("exit"),
		helper("block"),
	}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "exited unexpectedly") {
		t.Fatalf("unexpected result: %v", err)
	}
}

func TestRunPropagatesChildFailure(t *testing.T) {
	err := run(context.Background(), []commandSpec{
		helper("fail"),
		helper("block"),
	}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("unexpected result: %v", err)
	}
}

func TestRunStopsChildrenOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	if err := run(ctx, []commandSpec{helper("block"), helper("block")}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
}

func helper(mode string) commandSpec {
	return commandSpec{
		path: os.Args[0],
		args: []string{"-test.run=TestHelperProcess"},
		env: []string{
			"ROBIN_LIVE_CONTROL_HELPER=1",
			"ROBIN_LIVE_CONTROL_MODE=" + mode,
		},
	}
}
