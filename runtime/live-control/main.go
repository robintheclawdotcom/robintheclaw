package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

const shutdownTimeout = 10 * time.Second

type commandSpec struct {
	path string
	args []string
	env  []string
}

type processResult struct {
	index int
	err   error
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	specs := []commandSpec{
		{path: "./runtime/live-evaluation/bin/live-evaluation"},
		{path: "./runtime/live-scheduler/bin/live-scheduler"},
	}
	if err := run(ctx, specs, os.Stdout, os.Stderr); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, specs []commandSpec, stdout, stderr io.Writer) error {
	if len(specs) == 0 {
		return errors.New("live control requires at least one child")
	}

	commands := make([]*exec.Cmd, 0, len(specs))
	results := make(chan processResult, len(specs))
	for index, spec := range specs {
		command := exec.Command(spec.path, spec.args...)
		command.Env = append(os.Environ(), spec.env...)
		command.Stdout = stdout
		command.Stderr = stderr
		if err := command.Start(); err != nil {
			stop(commands)
			if !wait(results, len(commands), shutdownTimeout) {
				kill(commands)
			}
			return fmt.Errorf("start %s: %w", spec.path, err)
		}
		commands = append(commands, command)
		go func(index int, command *exec.Cmd) {
			results <- processResult{index: index, err: command.Wait()}
		}(index, command)
	}

	select {
	case <-ctx.Done():
		stop(commands)
		if !wait(results, len(commands), shutdownTimeout) {
			kill(commands)
			return errors.New("live control children did not stop before the shutdown deadline")
		}
		return nil
	case result := <-results:
		stop(commands)
		if !wait(results, len(commands)-1, shutdownTimeout) {
			kill(commands)
			return errors.New("live control child failed and its peer did not stop")
		}
		if result.err == nil {
			return fmt.Errorf("live control child %d exited unexpectedly", result.index)
		}
		return fmt.Errorf("live control child %d failed: %w", result.index, result.err)
	}
}

func stop(commands []*exec.Cmd) {
	for _, command := range commands {
		if command.Process != nil {
			_ = command.Process.Signal(syscall.SIGTERM)
		}
	}
}

func kill(commands []*exec.Cmd) {
	for _, command := range commands {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
	}
}

func wait(results <-chan processResult, count int, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for range count {
		select {
		case <-results:
		case <-timer.C:
			return false
		}
	}
	return true
}
