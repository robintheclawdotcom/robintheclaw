package aaplrelay

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

type Metrics struct {
	ready         atomic.Bool
	sourceHealthy atomic.Bool
	pending       atomic.Bool
	lastCycle     atomic.Int64
	lastConfirmed atomic.Int64
	sourceRound   atomic.Uint64
	sourceUpdated atomic.Int64
	failures      atomic.Uint64
	confirmed     atomic.Uint64
}

func MetricsHandler(metrics *Metrics, interval time.Duration) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(writer http.ResponseWriter, _ *http.Request) {
		fresh := time.Since(time.Unix(metrics.lastCycle.Load(), 0)) <= 3*interval
		if !metrics.ready.Load() || !metrics.sourceHealthy.Load() || !fresh {
			http.Error(writer, "not ready", http.StatusServiceUnavailable)
			return
		}
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/metrics", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = fmt.Fprintf(
			writer,
			"aapl_relay_ready %s\n"+
				"aapl_relay_source_healthy %s\n"+
				"aapl_relay_transaction_pending %s\n"+
				"aapl_relay_failures_total %d\n"+
				"aapl_relay_reports_confirmed_total %d\n"+
				"aapl_relay_last_cycle_timestamp_seconds %d\n"+
				"aapl_relay_last_confirmed_timestamp_seconds %d\n"+
				"aapl_relay_source_round %d\n"+
				"aapl_relay_source_updated_timestamp_seconds %d\n",
			boolNumber(metrics.ready.Load()), boolNumber(metrics.sourceHealthy.Load()),
			boolNumber(metrics.pending.Load()), metrics.failures.Load(), metrics.confirmed.Load(),
			metrics.lastCycle.Load(), metrics.lastConfirmed.Load(), metrics.sourceRound.Load(),
			metrics.sourceUpdated.Load(),
		)
	})
	return mux
}

func boolNumber(value bool) string {
	if value {
		return "1"
	}
	return "0"
}
