package sequencerpublisher

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
)

func MetricsHandler(metrics *Metrics, interval time.Duration) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(response http.ResponseWriter, _ *http.Request) {
		lastCycle := time.Unix(metrics.lastCycle.Load(), 0)
		if !metrics.ready.Load() || lastCycle.IsZero() || time.Since(lastCycle) > 3*interval {
			http.Error(response, "not ready", http.StatusServiceUnavailable)
			return
		}
		response.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = response.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/metrics", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = fmt.Fprintf(response,
			"sequencer_publisher_ready %s\nsequencer_source_healthy %s\nsequencer_transaction_pending %s\nsequencer_publish_failures_total %d\nsequencer_reports_confirmed_total %d\nsequencer_last_cycle_timestamp_seconds %d\nsequencer_last_confirmed_timestamp_seconds %d\n",
			boolMetric(metrics.ready.Load()), boolMetric(metrics.sourceHealthy.Load()), boolMetric(metrics.pending.Load()),
			metrics.failures.Load(), metrics.confirmed.Load(), metrics.lastCycle.Load(), metrics.lastConfirmed.Load())
	})
	return mux
}

func boolMetric(value bool) string {
	return strconv.FormatInt(map[bool]int64{false: 0, true: 1}[value], 10)
}
