use std::fmt::Write;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;

#[derive(Clone, Default)]
pub struct Metrics {
    inner: Arc<Inner>,
}

#[derive(Default)]
struct Inner {
    requests: AtomicU64,
    auth_failures: AtomicU64,
    database_failures: AtomicU64,
}

impl Metrics {
    pub fn record_request(&self) {
        self.inner.requests.fetch_add(1, Ordering::Relaxed);
    }

    pub fn record_auth_failure(&self) {
        self.inner.auth_failures.fetch_add(1, Ordering::Relaxed);
    }

    pub fn record_database_failure(&self) {
        self.inner.database_failures.fetch_add(1, Ordering::Relaxed);
    }

    pub fn encode(&self) -> String {
        let mut output = String::with_capacity(512);
        append_counter(
            &mut output,
            "robin_control_http_requests_total",
            "HTTP requests received by the control plane.",
            self.inner.requests.load(Ordering::Relaxed),
        );
        append_counter(
            &mut output,
            "robin_control_auth_failures_total",
            "Rejected control-plane authentication attempts.",
            self.inner.auth_failures.load(Ordering::Relaxed),
        );
        append_counter(
            &mut output,
            "robin_control_database_failures_total",
            "Failed control-plane database operations.",
            self.inner.database_failures.load(Ordering::Relaxed),
        );
        output
    }
}

fn append_counter(output: &mut String, name: &str, help: &str, value: u64) {
    let _ = writeln!(output, "# HELP {name} {help}");
    let _ = writeln!(output, "# TYPE {name} counter");
    let _ = writeln!(output, "{name} {value}");
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn metrics_are_valid_prometheus_counters() {
        let metrics = Metrics::default();
        metrics.record_request();
        metrics.record_auth_failure();
        let encoded = metrics.encode();
        assert!(encoded.contains("robin_control_http_requests_total 1"));
        assert!(encoded.contains("robin_control_auth_failures_total 1"));
    }
}
