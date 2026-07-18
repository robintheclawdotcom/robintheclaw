use anyhow::Context;
use chrono::Utc;
use robin_runtime::{
    agents::AgentStore,
    paper::{evaluate, validate_aapl_strategy_policy, PaperConfig, RobinhoodReader},
    storage::{PaperRecordOutcome, PaperStore},
};
use std::{env, time::Duration};
use tokio::{
    signal,
    time::{interval, sleep, MissedTickBehavior},
};
use tracing::{error, info};

const CONFIG_PATH: &str = "config/mainnet-paper.json";

#[derive(Default)]
struct Counters {
    events: u64,
    candidates: u64,
    declines: u64,
    opened: u64,
    marked: u64,
    closed: u64,
    dependency_errors: u64,
    superseded_events: u64,
}

impl Counters {
    fn record(&mut self, candidate: bool, outcome: PaperRecordOutcome) {
        self.superseded_events += outcome.superseded_events;
        if !outcome.inserted {
            return;
        }
        self.events += 1;
        if candidate {
            self.candidates += 1;
        } else {
            self.declines += 1;
        }
        self.opened += u64::from(outcome.episode_opened);
        self.marked += u64::from(outcome.episode_marked);
        self.closed += u64::from(outcome.episode_closed);
    }

    fn report(&self) {
        info!(
            events = self.events,
            candidates = self.candidates,
            declines = self.declines,
            opened = self.opened,
            marked = self.marked,
            closed = self.closed,
            dependency_errors = self.dependency_errors,
            superseded_events = self.superseded_events,
            "paper agent counters"
        );
    }
}

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    robin_runtime::install_tls_provider()?;
    tracing_subscriber::fmt()
        .with_env_filter(tracing_subscriber::EnvFilter::from_default_env())
        .with_target(false)
        .init();

    let config_path = env::var("PAPER_AGENT_CONFIG").unwrap_or_else(|_| CONFIG_PATH.to_string());
    let mut config = PaperConfig::load(&config_path)?;
    let minimum_net_edge_ppm = env::var("AAPL_MINIMUM_NET_EDGE_PPM")
        .context("AAPL_MINIMUM_NET_EDGE_PPM is required")?
        .parse()
        .context("AAPL_MINIMUM_NET_EDGE_PPM must be an integer")?;
    let strategy_policy_salt =
        env::var("AAPL_STRATEGY_POLICY_SALT").context("AAPL_STRATEGY_POLICY_SALT is required")?;
    validate_aapl_strategy_policy(minimum_net_edge_ppm, &strategy_policy_salt)?;
    config.set_minimum_net_edge_ppm(minimum_net_edge_ppm)?;
    let rpc_url = env::var("ROBINHOOD_RPC_URL").context("ROBINHOOD_RPC_URL is required")?;
    let reader = RobinhoodReader::new(rpc_url)?;
    reader.verify_chain(config.chain_id).await?;
    let store = PaperStore::from_env().await?;
    let agents = AgentStore::from_env().await?;
    let consumer = format!("paper-agent:{}", config.strategy_version);
    let symbols = config.symbols();
    store
        .initialize_cursor(&consumer, &symbols, Utc::now())
        .await?;
    let mut counters = Counters::default();
    let mut report = interval(Duration::from_secs(60));
    report.set_missed_tick_behavior(MissedTickBehavior::Skip);
    report.tick().await;

    info!(
        strategy_version = config.strategy_version,
        market_count = symbols.len(),
        chain_id = config.chain_id,
        "paper agent started"
    );

    loop {
        tokio::select! {
            _ = signal::ctrl_c() => {
                counters.report();
                info!("paper agent stopped");
                return Ok(());
            }
            _ = report.tick() => counters.report(),
            result = process_next(&store, agents.as_ref(), &reader, &config, &consumer, &symbols) => {
                match result {
                    Ok(Some((candidate, outcome))) => counters.record(candidate, outcome),
                    Ok(None) => sleep(Duration::from_millis(250)).await,
                    Err(error) => {
                        counters.dependency_errors += 1;
                        error!(error = %error, "paper evaluation deferred");
                        sleep(Duration::from_secs(1)).await;
                    }
                }
            }
        }
    }
}

async fn process_next(
    store: &PaperStore,
    agents: Option<&AgentStore>,
    reader: &RobinhoodReader,
    config: &PaperConfig,
    consumer: &str,
    symbols: &[String],
) -> anyhow::Result<Option<(bool, PaperRecordOutcome)>> {
    if let Some(agents) = agents {
        if let Some(fanout) = store.next_agent_fanout().await? {
            match agents.record_evaluation(&fanout).await {
                Ok(_) => {
                    store
                        .mark_agent_fanout_delivered(fanout.evaluation_id)
                        .await?
                }
                Err(error) => {
                    store
                        .record_agent_fanout_error(fanout.evaluation_id, &error.to_string())
                        .await?;
                    return Err(error);
                }
            }
        }
    }
    let Some(event) = store.next_ticker(consumer, symbols).await? else {
        return Ok(None);
    };
    let market = config
        .market(&event.symbol)
        .with_context(|| format!("paper market {} is not configured", event.symbol))?;
    let active = store
        .active_position(&config.strategy_version, &event.symbol)
        .await?;
    let snapshot = reader.snapshot(config, market, active.as_ref()).await?;
    let evaluation = evaluate(
        config,
        market,
        &event,
        &snapshot,
        active.as_ref(),
        Utc::now(),
    );
    let candidate = evaluation.is_candidate();
    let outcome = store
        .record_paper_evaluation(consumer, &config.strategy_version, &event, &evaluation)
        .await?;
    Ok(Some((candidate, outcome)))
}
