package publisher

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxPublisherMetricAccounts = 512

var publisherMetricTypes = map[string]string{
	"robin_gas_balance_wei":           "gauge",
	"robin_gas_ready":                 "gauge",
	"robin_margin_coverage_ratio":     "gauge",
	"robin_rpc_finality_disagreement": "gauge",
	"robin_source_age_seconds":        "gauge",
	"robin_source_gap_open":           "gauge",
	"robin_unknown_orders":            "gauge",
	"robin_unknown_positions":         "gauge",
}

type publisherMetrics struct {
	mu          sync.Mutex
	environment string
	accounts    map[string]publisherAccountMetrics
	now         func() time.Time
}

type publisherAccountMetrics struct {
	strategy       string
	lighter        LighterObservation
	robinhood      RobinhoodObservation
	lighterGapOpen bool
}

func newPublisherMetrics(environment string) *publisherMetrics {
	return &publisherMetrics{
		environment: environment,
		accounts:    make(map[string]publisherAccountMetrics),
		now:         time.Now,
	}
}

func validMetricLabel(value string) bool {
	if len(value) < 2 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' && character != '_' {
			return false
		}
	}
	return true
}

func (value *publisherMetrics) BeginCycle(accounts []AccountBinding) {
	value.mu.Lock()
	defer value.mu.Unlock()

	sorted := append([]AccountBinding(nil), accounts...)
	sort.Slice(sorted, func(left, right int) bool {
		return sorted[left].ExecutionAccountID < sorted[right].ExecutionAccountID
	})
	if len(sorted) > maxPublisherMetricAccounts {
		sorted = sorted[:maxPublisherMetricAccounts]
	}
	allowed := make(map[string]AccountBinding, len(sorted))
	for _, account := range sorted {
		allowed[account.ExecutionAccountID] = account
	}
	for id := range value.accounts {
		if _, exists := allowed[id]; !exists {
			delete(value.accounts, id)
		}
	}
	for id, account := range allowed {
		state, exists := value.accounts[id]
		if !exists {
			state.lighterGapOpen = true
		}
		state.strategy = account.StrategyVersion
		value.accounts[id] = state
	}
}

func (value *publisherMetrics) Observe(account AccountBinding, lighter LighterObservation, robinhood RobinhoodObservation) {
	value.mu.Lock()
	defer value.mu.Unlock()
	state, exists := value.accounts[account.ExecutionAccountID]
	if !exists {
		return
	}
	state.strategy = account.StrategyVersion
	state.lighter = lighter
	state.robinhood = robinhood
	state.lighterGapOpen = !lighter.RESTReconstructed
	value.accounts[account.ExecutionAccountID] = state
}

func (value *publisherMetrics) SourceFailure(executionID, venue string) {
	value.mu.Lock()
	defer value.mu.Unlock()
	state, exists := value.accounts[executionID]
	if !exists {
		return
	}
	if venue == "lighter" {
		state.lighterGapOpen = true
	}
	value.accounts[executionID] = state
}

func (value *publisherMetrics) Handler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = writer.Write([]byte(value.render()))
	})
}

func (value *publisherMetrics) render() string {
	value.mu.Lock()
	defer value.mu.Unlock()

	now := value.now().UTC()
	var output strings.Builder
	names := make([]string, 0, len(publisherMetricTypes))
	for name := range publisherMetricTypes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&output, "# TYPE %s %s\n", name, publisherMetricTypes[name])
	}

	ids := make([]string, 0, len(value.accounts))
	for id := range value.accounts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		state := value.accounts[id]
		common := map[string]string{
			"environment":          value.environment,
			"execution_account_id": id,
			"strategy_version":     state.strategy,
		}
		if !state.lighter.ObservedAt.IsZero() {
			writePromMetric(&output, "robin_source_age_seconds", cloneLabels(common, map[string]string{
				"venue": "lighter", "source": "authenticated-rest",
			}), durationSeconds(now.Sub(state.lighter.ObservedAt)))
			writePromMetric(&output, "robin_margin_coverage_ratio", cloneLabels(common, map[string]string{
				"venue": "lighter",
			}), strconv.FormatFloat(float64(state.lighter.MaintenanceMarginRatioMicros)/1_000_000, 'f', 6, 64))
			writePromMetric(&output, "robin_unknown_positions", cloneLabels(common, map[string]string{
				"venue": "lighter",
			}), boolCount(!state.lighter.NoUnknownPositions))
			writePromMetric(&output, "robin_unknown_orders", cloneLabels(common, map[string]string{
				"venue": "lighter",
			}), boolCount(!state.lighter.NoUnknownOrders))
		}
		writePromMetric(&output, "robin_source_gap_open", cloneLabels(common, map[string]string{
			"venue": "lighter", "stream": "authenticated-account",
		}), boolCount(state.lighterGapOpen))

		if state.robinhood.ObservedAt.IsZero() {
			continue
		}
		writePromMetric(&output, "robin_source_age_seconds", cloneLabels(common, map[string]string{
			"venue": "robinhood", "source": "dual-rpc-finality",
		}), durationSeconds(now.Sub(state.robinhood.ObservedAt)))
		writePromMetric(&output, "robin_rpc_finality_disagreement", cloneLabels(common, map[string]string{
			"chain_id": "4663", "receipt": "aggregate",
		}), boolCount(!state.robinhood.FinalityHealthy))
		wallets := []struct {
			role    string
			balance string
			ready   bool
		}{
			{role: "user", balance: state.robinhood.OwnerGasRaw, ready: state.robinhood.OwnerGasReady},
			{role: "execution_signer", balance: state.robinhood.SignerGasRaw, ready: state.robinhood.SignerGasReady},
		}
		for _, wallet := range wallets {
			labels := cloneLabels(common, map[string]string{"wallet_role": wallet.role, "chain_id": "4663"})
			writePromMetric(&output, "robin_gas_balance_wei", labels, wallet.balance)
			writePromMetric(&output, "robin_gas_ready", labels, boolCount(wallet.ready))
		}
	}
	return output.String()
}

func cloneLabels(base, extra map[string]string) map[string]string {
	result := make(map[string]string, len(base)+len(extra))
	for key, item := range base {
		result[key] = item
	}
	for key, item := range extra {
		result[key] = item
	}
	return result
}

func writePromMetric(output *strings.Builder, name string, labels map[string]string, metricValue string) {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	output.WriteString(name)
	output.WriteByte('{')
	for index, key := range keys {
		if index > 0 {
			output.WriteByte(',')
		}
		fmt.Fprintf(output, "%s=%q", key, labels[key])
	}
	output.WriteString("} ")
	output.WriteString(metricValue)
	output.WriteByte('\n')
}

func durationSeconds(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	return strconv.FormatFloat(duration.Seconds(), 'f', 3, 64)
}

func boolCount(value bool) string {
	if value {
		return "1"
	}
	return "0"
}
