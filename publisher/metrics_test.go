package publisher

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

type metricsContract struct {
	CommonLabels []string `json:"commonLabels"`
	Metrics      []struct {
		Name   string   `json:"name"`
		Type   string   `json:"type"`
		Labels []string `json:"labels"`
	} `json:"metrics"`
}

func TestPublisherMetricsMatchContract(t *testing.T) {
	contractBody, err := os.ReadFile("../ops/mainnet-live/metrics/contract.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract metricsContract
	if err := json.Unmarshal(contractBody, &contract); err != nil {
		t.Fatal(err)
	}
	specs := make(map[string]struct {
		metricType string
		labels     []string
	}, len(contract.Metrics))
	for _, metric := range contract.Metrics {
		labels := append(append([]string(nil), contract.CommonLabels...), metric.Labels...)
		sort.Strings(labels)
		specs[metric.Name] = struct {
			metricType string
			labels     []string
		}{metric.Type, labels}
	}

	metrics := populatedPublisherMetrics()
	rendered := metrics.render()
	types := make(map[string]string)
	emitted := make(map[string]bool)
	for _, line := range strings.Split(rendered, "\n") {
		if strings.HasPrefix(line, "# TYPE ") {
			fields := strings.Fields(line)
			if len(fields) != 4 {
				t.Fatalf("invalid type declaration %q", line)
			}
			types[fields[2]] = fields[3]
			continue
		}
		if line == "" {
			continue
		}
		open, close := strings.IndexByte(line, '{'), strings.IndexByte(line, '}')
		if open <= 0 || close <= open {
			t.Fatalf("invalid metric line %q", line)
		}
		name := line[:open]
		spec, exists := specs[name]
		if !exists {
			t.Fatalf("metric %s is not in the contract", name)
		}
		labels := metricLabelNames(t, line[open+1:close])
		if strings.Join(labels, ",") != strings.Join(spec.labels, ",") {
			t.Fatalf("%s labels=%v want=%v", name, labels, spec.labels)
		}
		if types[name] != spec.metricType {
			t.Fatalf("%s type=%q want=%q", name, types[name], spec.metricType)
		}
		emitted[name] = true
	}
	if len(emitted) != len(publisherMetricTypes) {
		t.Fatalf("emitted=%v configured=%v", emitted, publisherMetricTypes)
	}
	for name, metricType := range publisherMetricTypes {
		if !emitted[name] || types[name] != metricType {
			t.Fatalf("metric %s was not emitted with type %s", name, metricType)
		}
	}
}

func TestPublisherMetricsAreBounded(t *testing.T) {
	metrics := newPublisherMetrics("production")
	metrics.now = func() time.Time { return time.Unix(2_000_000_010, 0) }
	accounts := make([]AccountBinding, 0, maxPublisherMetricAccounts+100)
	for index := 0; index < maxPublisherMetricAccounts+100; index++ {
		accounts = append(accounts, AccountBinding{
			ExecutionAccountID: fmt.Sprintf("%08d-0000-4000-8000-%012d", index, index),
			StrategyVersion:    "basis-aapl-v1",
		})
	}
	metrics.BeginCycle(accounts)
	for _, account := range accounts {
		metrics.Observe(account, LighterObservation{
			NoUnknownOrders: true, NoUnknownPositions: true, RESTReconstructed: true,
			ObservedAt: time.Unix(2_000_000_000, 0),
		}, RobinhoodObservation{})
	}
	if got := strings.Count(metrics.render(), "robin_margin_coverage_ratio{"); got != maxPublisherMetricAccounts {
		t.Fatalf("margin series=%d want=%d", got, maxPublisherMetricAccounts)
	}
}

func TestPublisherMetricsFailGapOpenUntilReconstructed(t *testing.T) {
	account := AccountBinding{ExecutionAccountID: "10000000-0000-4000-8000-000000000001", StrategyVersion: "basis-aapl-v1"}
	metrics := newPublisherMetrics("production")
	metrics.BeginCycle([]AccountBinding{account})
	if !strings.Contains(metrics.render(), `robin_source_gap_open{environment="production",execution_account_id="10000000-0000-4000-8000-000000000001",strategy_version="basis-aapl-v1",stream="authenticated-account",venue="lighter"} 1`) {
		t.Fatal("new account did not fail gap open")
	}
	metrics.Observe(account, LighterObservation{RESTReconstructed: true, ObservedAt: time.Now()}, RobinhoodObservation{})
	if !strings.Contains(metrics.render(), `stream="authenticated-account",venue="lighter"} 0`) {
		t.Fatal("successful reconstruction did not close gap")
	}
	metrics.SourceFailure(account.ExecutionAccountID, "lighter")
	if !strings.Contains(metrics.render(), `stream="authenticated-account",venue="lighter"} 1`) {
		t.Fatal("source failure did not reopen gap")
	}
	metrics.BeginCycle(nil)
	if strings.Contains(metrics.render(), account.ExecutionAccountID) {
		t.Fatal("removed account remained in metrics")
	}
}

func TestMetricsEndpointIsSeparateFromHealth(t *testing.T) {
	service := &Service{
		config:  Config{Enabled: true, PollInterval: 4500 * time.Millisecond},
		metrics: newPublisherMetrics("production"),
	}
	metricsResponse := httptest.NewRecorder()
	service.HealthHandler().ServeHTTP(metricsResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if metricsResponse.Code != http.StatusOK || !strings.Contains(metricsResponse.Body.String(), "robin_source_age_seconds") {
		t.Fatalf("metrics status=%d body=%s", metricsResponse.Code, metricsResponse.Body.String())
	}
	if metricsResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("metrics cache control=%q", metricsResponse.Header().Get("Cache-Control"))
	}
	healthResponse := httptest.NewRecorder()
	service.HealthHandler().ServeHTTP(healthResponse, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if strings.Contains(healthResponse.Body.String(), "robin_") {
		t.Fatalf("health endpoint exposed metrics: %s", healthResponse.Body.String())
	}
}

func populatedPublisherMetrics() *publisherMetrics {
	metrics := newPublisherMetrics("production")
	metrics.now = func() time.Time { return time.Unix(2_000_000_010, 0) }
	account := AccountBinding{
		ExecutionAccountID: "10000000-0000-4000-8000-000000000001",
		StrategyVersion:    "basis-aapl-v1",
	}
	metrics.BeginCycle([]AccountBinding{account})
	metrics.Observe(account, LighterObservation{
		MaintenanceMarginRatioMicros: 2_500_000,
		NoUnknownOrders:              true,
		NoUnknownPositions:           true,
		RESTReconstructed:            true,
		ObservedAt:                   time.Unix(2_000_000_000, 0),
	}, RobinhoodObservation{
		OwnerGasRaw: "100", SignerGasRaw: "200", OwnerGasReady: true, SignerGasReady: true,
		FinalityHealthy: true, ObservedAt: time.Unix(2_000_000_000, 0),
	})
	return metrics
}

func metricLabelNames(t *testing.T, labels string) []string {
	t.Helper()
	var result []string
	for _, label := range strings.Split(labels, ",") {
		key, _, exists := strings.Cut(label, "=")
		if !exists || key == "" {
			t.Fatalf("invalid metric label %q", label)
		}
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}
