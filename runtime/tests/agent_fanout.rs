use chrono::Utc;
use robin_runtime::agents::{AgentFanout, AgentStore};
use sqlx::postgres::PgPoolOptions;
use uuid::Uuid;

#[tokio::test]
async fn running_agents_receive_each_evaluation_once() -> anyhow::Result<()> {
    let Ok(database_url) = std::env::var("AGENT_TEST_DATABASE_URL") else {
        return Ok(());
    };
    let pool = PgPoolOptions::new().connect(&database_url).await?;
    let user_id = Uuid::new_v4();
    let agent_id = Uuid::new_v4();
    sqlx::query("INSERT INTO users (id, privy_did) VALUES ($1, $2)")
        .bind(user_id)
        .bind(format!("did:agent-test:{user_id}"))
        .execute(&pool)
        .await?;
    sqlx::query(
        "INSERT INTO agents (id, user_id, strategy_version, mode, status) \
         VALUES ($1, $2, 'basis-paper-v1', 'paper', 'running')",
    )
    .bind(agent_id)
    .bind(user_id)
    .execute(&pool)
    .await?;

    let fanout = AgentFanout {
        evaluation_id: Uuid::new_v4(),
        market_event_id: Uuid::new_v4(),
        strategy_version: "basis-paper-v1".to_string(),
        episode_id: None,
        symbol: "AAPL".to_string(),
        status: "declined".to_string(),
        reason: Some("net_edge_below_threshold".to_string()),
        net_edge_ppm: Some(50),
        evaluated_at: Utc::now(),
    };
    let store = AgentStore::connect(&database_url).await?;
    assert_eq!(store.record_evaluation(&fanout).await?, 1);
    assert_eq!(store.record_evaluation(&fanout).await?, 0);
    let count =
        sqlx::query_scalar::<_, i64>("SELECT count(*) FROM agent_paper_events WHERE agent_id = $1")
            .bind(agent_id)
            .fetch_one(&pool)
            .await?;
    assert_eq!(count, 1);

    sqlx::query("DELETE FROM users WHERE id = $1")
        .bind(user_id)
        .execute(&pool)
        .await?;
    Ok(())
}
