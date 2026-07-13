# Offline research pipeline

This package implements the slow research loop. It establishes the net-cost baseline, tests
cointegration and mean reversion, classifies market regimes, estimates shrinkage covariance,
sizes a constrained fractional-Kelly portfolio, and produces frozen model artifacts.

Research input is a verified dataset manifest. Execution never imports this package; it consumes
only reviewed artifacts whose hashes and evidence have passed the Rust promotion gate.

```bash
UV_CACHE_DIR=/tmp/robin-uv uv sync --frozen
UV_CACHE_DIR=/tmp/robin-uv uv run pytest
UV_CACHE_DIR=/tmp/robin-uv uv run ruff check .
```
