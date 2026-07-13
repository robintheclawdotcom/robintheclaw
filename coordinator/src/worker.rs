use crate::{
    signer_client::{
        LighterCreateOrderRequest, LighterTransactionOptions, RobinhoodExecuteRequest,
        RobinhoodSpotIntent, RobinhoodSubmission, SignedLighterTransaction, SignerClientError,
        SignerClients,
    },
    store::{
        ActionKind, ActionStop, ClaimedAction, NextAction, ObservationOutcome, PerpObservation,
        SpotObservation, Store, StoreError,
    },
};
use execution::{ExecutionEvent, ExecutionState};
use serde::Deserialize;
use serde_json::{json, Value};
use std::time::{Duration, SystemTime, UNIX_EPOCH};
use thiserror::Error;
use tokio::sync::watch;

const LEASE_DURATION: Duration = Duration::from_secs(30);
const IDLE_POLL: Duration = Duration::from_millis(250);

#[derive(Clone)]
pub struct Worker {
    store: Store,
    signers: SignerClients,
    worker_id: String,
    lighter_account_index: i64,
    lighter_api_key_index: u8,
}

#[derive(Debug, Error)]
pub enum WorkerError {
    #[error(transparent)]
    Store(#[from] StoreError),
    #[error("execution action payload is invalid")]
    InvalidPayload,
}

#[derive(Deserialize)]
struct FilledBase {
    filled_base: u64,
}

#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
struct ExitAuthority {
    quote_source_session: String,
    quote_source_event_id: String,
    quote_expires_at_ms: u64,
    perp_mark_price: u32,
    perp_unwind_price: u32,
    spot_amount_in: String,
    minimum_unwind_settlement_out: String,
    submission_deadline_ms: u64,
    reconciliation_deadline_ms: u64,
}

impl Worker {
    pub fn new(
        store: Store,
        signers: SignerClients,
        worker_id: String,
        lighter_account_index: i64,
        lighter_api_key_index: u8,
    ) -> Self {
        Self {
            store,
            signers,
            worker_id,
            lighter_account_index,
            lighter_api_key_index,
        }
    }

    pub async fn run(self, mut shutdown: watch::Receiver<bool>) {
        loop {
            if *shutdown.borrow() {
                return;
            }
            match self
                .store
                .claim_action(&self.worker_id, LEASE_DURATION)
                .await
            {
                Ok(Some(action)) => {
                    let claimed = action.clone();
                    if let Err(error) = self.process(action).await {
                        if is_poison_error(&error) {
                            if let Err(stop_error) = self
                                .store
                                .fail_safe_action(
                                    &claimed.id,
                                    &self.worker_id,
                                    &claimed.lease_token,
                                    "worker_payload_invalid",
                                    json!({"action_kind": claimed.kind}),
                                )
                                .await
                            {
                                tracing::error!(error = %stop_error, "failed to quarantine execution action");
                            }
                        }
                        tracing::error!(error = %error, "execution action failed");
                    }
                }
                Ok(None) => {
                    tokio::select! {
                        _ = tokio::time::sleep(IDLE_POLL) => {}
                        changed = shutdown.changed() => {
                            if changed.is_err() || *shutdown.borrow() {
                                return;
                            }
                        }
                    }
                }
                Err(error) => {
                    tracing::error!(error = %error, "execution queue unavailable");
                    tokio::time::sleep(Duration::from_secs(1)).await;
                }
            }
        }
    }

    async fn process(&self, action: ClaimedAction) -> Result<(), WorkerError> {
        match action.kind {
            ActionKind::SubmitPerp => self.submit_perp(action, false).await,
            ActionKind::ReconcilePerp => self.reconcile_perp(action).await,
            ActionKind::SubmitSpot => self.submit_spot(action).await,
            ActionKind::ReconcileSpot => self.reconcile_spot(action).await,
            ActionKind::UnwindPerp => self.submit_perp(action, true).await,
            ActionKind::ReconcileUnwind => self.reconcile_unwind(action).await,
            ActionKind::UnwindSpot => self.unwind_spot(action).await,
            ActionKind::ReconcileUnwindSpot => self.reconcile_unwind_spot(action).await,
        }
    }

    async fn submit_perp(&self, action: ClaimedAction, unwind: bool) -> Result<(), WorkerError> {
        if let Some(submission) = saved_result::<Value>(&action.result, "submission") {
            self.complete_lighter_submission(&action, unwind, submission)
                .await?;
            return Ok(());
        }

        let now = now_ms()?;
        if unwind && exit_authority(&action).is_err() {
            if now > action.intent.emergency_deadline_ms {
                self.store
                    .stop_action(
                        &action.id,
                        &self.worker_id,
                        &action.lease_token,
                        ActionStop::FailedSafe,
                        "exit_authority_overdue",
                        None,
                        json!({"stage": "perp_unwind_authority"}),
                    )
                    .await?;
                return Ok(());
            }
            let bound = self
                .store
                .bind_exit_authority(&action.id, &self.worker_id, &action.lease_token, now)
                .await?;
            self.retry(
                &action,
                if bound {
                    "exit_authority_bound"
                } else {
                    "awaiting_exit_authority"
                },
            )
            .await?;
            return Ok(());
        }
        let recovered_signed = saved_result::<SignedLighterTransaction>(&action.result, "signed");
        let send_authorized = lighter_send_authorized(&action.result);
        if unwind && now > unwind_submission_deadline_ms(&action) {
            if send_authorized {
                if let Some(signed) = recovered_signed.as_ref() {
                    self.continue_lighter_reconciliation(&action, true, &signed.tx_hash)
                        .await?;
                    return Ok(());
                }
            }
            self.store
                .stop_action(
                    &action.id,
                    &self.worker_id,
                    &action.lease_token,
                    ActionStop::FailedSafe,
                    "perp_unwind_expired",
                    Some(ExecutionEvent::Unhedged),
                    json!({"stage": "perp_unwind_submission"}),
                )
                .await?;
            return Ok(());
        }
        if !unwind && now > action.intent.deadline_ms {
            if send_authorized {
                if let Some(signed) = recovered_signed.as_ref() {
                    self.continue_lighter_reconciliation(&action, false, &signed.tx_hash)
                        .await?;
                    return Ok(());
                }
            }
            self.store
                .stop_action(
                    &action.id,
                    &self.worker_id,
                    &action.lease_token,
                    ActionStop::Rejected,
                    "intent_expired",
                    Some(ExecutionEvent::Expired),
                    json!({"stage": "perp_submission"}),
                )
                .await?;
            return Ok(());
        }
        let signed = match recovered_signed {
            Some(signed) => signed,
            None => {
                let observed = match self
                    .signers
                    .fetch_lighter_nonce(self.lighter_account_index, self.lighter_api_key_index)
                    .await
                {
                    Ok(nonce) => nonce,
                    Err(_) => {
                        self.retry(&action, "lighter_nonce_unavailable").await?;
                        return Ok(());
                    }
                };
                let nonce = self
                    .store
                    .assign_lighter_nonce(
                        &action.id,
                        &self.worker_id,
                        &action.lease_token,
                        self.lighter_account_index,
                        self.lighter_api_key_index,
                        observed,
                    )
                    .await?;
                let request = lighter_request(&action, nonce, unwind)?;
                match self.signers.sign_lighter_create_order(&request).await {
                    Ok(signed) => {
                        self.store
                            .record_action_result(
                                &action.id,
                                &self.worker_id,
                                &action.lease_token,
                                "signed",
                                serde_json::to_value(&signed)
                                    .map_err(|_| WorkerError::InvalidPayload)?,
                            )
                            .await?;
                        signed
                    }
                    Err(error) if retryable_lighter_signer_error(&error) => {
                        self.retry(&action, "lighter_signer_unavailable").await?;
                        return Ok(());
                    }
                    Err(error) => {
                        let transition = if unwind {
                            ExecutionEvent::Unhedged
                        } else {
                            ExecutionEvent::Cancelled
                        };
                        self.store
                            .stop_action(
                                &action.id,
                                &self.worker_id,
                                &action.lease_token,
                                if unwind {
                                    ActionStop::FailedSafe
                                } else {
                                    ActionStop::Rejected
                                },
                                "lighter_signing_rejected",
                                Some(transition),
                                json!({"classification": error.to_string()}),
                            )
                            .await?;
                        return Ok(());
                    }
                }
            }
        };

        match self
            .store
            .validate_lighter_nonce_binding(
                &action.id,
                self.lighter_account_index,
                self.lighter_api_key_index,
            )
            .await
        {
            Ok(()) => {}
            Err(StoreError::LighterConfigDrift) => {
                self.retry(&action, "lighter_nonce_scope_drift").await?;
                return Ok(());
            }
            Err(error) => return Err(error.into()),
        }
        if !unwind {
            match self
                .store
                .authorize_entry_send(
                    &action.id,
                    &self.worker_id,
                    &action.lease_token,
                    action.control_version,
                )
                .await
            {
                Ok(()) => {}
                Err(StoreError::CoordinatorHalted) => {
                    if send_authorized {
                        self.continue_lighter_reconciliation(&action, false, &signed.tx_hash)
                            .await?;
                    } else {
                        self.store
                            .stop_action(
                                &action.id,
                                &self.worker_id,
                                &action.lease_token,
                                ActionStop::Rejected,
                                "entry_send_not_authorized",
                                Some(ExecutionEvent::Cancelled),
                                json!({"stage": "entry_send_authorization"}),
                            )
                            .await?;
                    }
                    return Ok(());
                }
                Err(error) => return Err(error.into()),
            }
        } else {
            match self
                .store
                .authorize_unwind_send(&action.id, &self.worker_id, &action.lease_token, now_ms()?)
                .await
            {
                Ok(()) => {}
                Err(StoreError::MarketAuthorityUnavailable) => {
                    if send_authorized {
                        self.continue_lighter_reconciliation(&action, true, &signed.tx_hash)
                            .await?;
                    } else {
                        self.store
                            .stop_action(
                                &action.id,
                                &self.worker_id,
                                &action.lease_token,
                                ActionStop::FailedSafe,
                                "exit_authority_expired",
                                None,
                                json!({"stage": "perp_unwind_send"}),
                            )
                            .await?;
                    }
                    return Ok(());
                }
                Err(error) => return Err(error.into()),
            }
        }
        match self.signers.broadcast_lighter(&signed).await {
            Ok(submission) => {
                let result =
                    serde_json::to_value(&submission).map_err(|_| WorkerError::InvalidPayload)?;
                self.store
                    .record_action_result(
                        &action.id,
                        &self.worker_id,
                        &action.lease_token,
                        "submission",
                        result.clone(),
                    )
                    .await?;
                self.complete_lighter_submission(&action, unwind, result)
                    .await?;
            }
            Err(SignerClientError::LighterRejected(code)) => {
                tracing::warn!(code, tx_hash = %signed.tx_hash, "Lighter submission requires reconciliation");
                self.continue_lighter_reconciliation(&action, unwind, &signed.tx_hash)
                    .await?;
            }
            Err(
                SignerClientError::AmbiguousLighterSubmission
                | SignerClientError::LighterHashMismatch,
            ) => {
                self.continue_lighter_reconciliation(&action, unwind, &signed.tx_hash)
                    .await?;
            }
            Err(SignerClientError::Encoding) => {
                self.store
                    .stop_action(
                        &action.id,
                        &self.worker_id,
                        &action.lease_token,
                        if unwind {
                            ActionStop::FailedSafe
                        } else {
                            ActionStop::Rejected
                        },
                        "lighter_submission_encoding_failed",
                        Some(if unwind {
                            ExecutionEvent::Unhedged
                        } else {
                            ExecutionEvent::Cancelled
                        }),
                        json!({"tx_hash": signed.tx_hash}),
                    )
                    .await?;
            }
            Err(_) => {
                self.continue_lighter_reconciliation(&action, unwind, &signed.tx_hash)
                    .await?;
            }
        }
        Ok(())
    }

    async fn complete_lighter_submission(
        &self,
        action: &ClaimedAction,
        unwind: bool,
        result: Value,
    ) -> Result<(), StoreError> {
        let filled_base = action.payload.get("filled_base").and_then(Value::as_u64);
        let tx_hash = result
            .get("tx_hash")
            .and_then(Value::as_str)
            .ok_or(StoreError::InvalidAction)?;
        let attempt = if unwind {
            u64::from(unwind_attempt(action).map_err(|_| StoreError::InvalidAction)?)
        } else {
            0
        };
        let recovery_order_index = if unwind {
            Some(unwind_client_order_index(action).map_err(|_| StoreError::InvalidAction)?)
        } else {
            None
        };
        let exit_authority = if unwind {
            Some(
                action
                    .payload
                    .get("exit_authority")
                    .cloned()
                    .ok_or(StoreError::InvalidAction)?,
            )
        } else {
            None
        };
        let operator_recovery = action.payload.get("operator_recovery").cloned();
        let client_order_index = action.payload.get("client_order_index").cloned();
        let payload = filled_base.map_or_else(
            || json!({"tx_hash": tx_hash}),
            |filled| {
                json!({
                    "filled_base": filled,
                    "tx_hash": tx_hash,
                    "unwind_attempt": attempt,
                    "unwound_before": action.saga.perp_unwound_base,
                    "exit_authority": exit_authority,
                    "operator_recovery": operator_recovery,
                    "client_order_index": client_order_index,
                })
            },
        );
        self.store
            .complete_action(
                &action.id,
                &self.worker_id,
                &action.lease_token,
                if unwind {
                    None
                } else {
                    Some(ExecutionEvent::PerpSubmitted)
                },
                result,
                Some(NextAction {
                    kind: if unwind {
                        ActionKind::ReconcileUnwind
                    } else {
                        ActionKind::ReconcilePerp
                    },
                    key: if unwind {
                        format!(
                            "reconcile-unwind-perp-{}",
                            recovery_order_index.ok_or(StoreError::InvalidAction)?
                        )
                    } else {
                        "reconcile-entry-perp".into()
                    },
                    payload,
                }),
            )
            .await?;
        Ok(())
    }

    async fn continue_lighter_reconciliation(
        &self,
        action: &ClaimedAction,
        unwind: bool,
        tx_hash: &str,
    ) -> Result<(), StoreError> {
        let filled_base = action.payload.get("filled_base").and_then(Value::as_u64);
        let attempt = if unwind {
            u64::from(unwind_attempt(action).map_err(|_| StoreError::InvalidAction)?)
        } else {
            0
        };
        let recovery_order_index = if unwind {
            Some(unwind_client_order_index(action).map_err(|_| StoreError::InvalidAction)?)
        } else {
            None
        };
        let exit_authority = if unwind {
            Some(
                action
                    .payload
                    .get("exit_authority")
                    .cloned()
                    .ok_or(StoreError::InvalidAction)?,
            )
        } else {
            None
        };
        let operator_recovery = action.payload.get("operator_recovery").cloned();
        let client_order_index = action.payload.get("client_order_index").cloned();
        let payload = filled_base.map_or_else(
            || json!({"tx_hash": tx_hash}),
            |filled| {
                json!({
                    "filled_base": filled,
                    "tx_hash": tx_hash,
                    "unwind_attempt": attempt,
                    "unwound_before": action.saga.perp_unwound_base,
                    "exit_authority": exit_authority,
                    "operator_recovery": operator_recovery,
                    "client_order_index": client_order_index,
                })
            },
        );
        self.store
            .continue_ambiguous_action(
                &action.id,
                &self.worker_id,
                &action.lease_token,
                "lighter_submission_ambiguous",
                (!unwind).then_some(ExecutionEvent::PerpSubmitted),
                json!({"tx_hash": tx_hash}),
                NextAction {
                    kind: if unwind {
                        ActionKind::ReconcileUnwind
                    } else {
                        ActionKind::ReconcilePerp
                    },
                    key: if unwind {
                        format!(
                            "reconcile-unwind-perp-{}",
                            recovery_order_index.ok_or(StoreError::InvalidAction)?
                        )
                    } else {
                        "reconcile-entry-perp".into()
                    },
                    payload,
                },
            )
            .await?;
        Ok(())
    }

    async fn reconcile_perp(&self, action: ClaimedAction) -> Result<(), WorkerError> {
        let Some(event) = self.store.next_venue_event(&action).await? else {
            if now_ms()? > action.intent.perp_order_expiry_ms {
                self.store.halt("perp_reconciliation_overdue").await?;
            }
            self.retry(&action, "awaiting_perp_event").await?;
            return Ok(());
        };
        let observation = match perp_observation(&action, &event.payload, false) {
            Ok(observation) => observation,
            Err(_) => {
                self.reject_observation(
                    &action,
                    event.id,
                    &event.kind,
                    ExecutionEvent::SafeFailure,
                    "perp_observation_mismatch",
                )
                .await?;
                return Ok(());
            }
        };
        let outcome = match event.kind.as_str() {
            "perp_accepted" => ObservationOutcome {
                transition: None,
                complete: false,
                result: event.payload.clone(),
                next: None,
            },
            "perp_partial" => {
                let fill = observation
                    .filled_base()
                    .ok_or(WorkerError::InvalidPayload)?;
                if !fill_within_cap(&action, &observation, fill) {
                    return self.overfilled_perp(&action, event.id, fill).await;
                }
                if fill < action.saga.perp_filled_base {
                    self.reject_observation(
                        &action,
                        event.id,
                        &event.kind,
                        ExecutionEvent::SafeFailure,
                        "perp_fill_regressed",
                    )
                    .await?;
                    return Ok(());
                }
                ObservationOutcome {
                    transition: (fill > action.saga.perp_filled_base)
                        .then_some(ExecutionEvent::PerpPartiallyFilled { filled_base: fill }),
                    complete: false,
                    result: event.payload.clone(),
                    next: None,
                }
            }
            "perp_filled" => {
                let fill = observation
                    .filled_base()
                    .ok_or(WorkerError::InvalidPayload)?;
                if !fill_within_cap(&action, &observation, fill) {
                    return self.overfilled_perp(&action, event.id, fill).await;
                }
                if fill < action.saga.perp_filled_base {
                    self.reject_observation(
                        &action,
                        event.id,
                        &event.kind,
                        ExecutionEvent::SafeFailure,
                        "perp_fill_regressed",
                    )
                    .await?;
                    return Ok(());
                }
                ObservationOutcome {
                    transition: Some(ExecutionEvent::PerpFilled { filled_base: fill }),
                    complete: true,
                    result: event.payload.clone(),
                    next: Some(NextAction {
                        kind: ActionKind::SubmitSpot,
                        key: "hedge-spot".into(),
                        payload: json!({"filled_base": fill}),
                    }),
                }
            }
            "perp_rejected" => {
                let fill = observation
                    .filled_base()
                    .ok_or(WorkerError::InvalidPayload)?;
                if fill > 0 && !fill_within_cap(&action, &observation, fill) {
                    return self.overfilled_perp(&action, event.id, fill).await;
                }
                if fill < action.saga.perp_filled_base {
                    self.reject_observation(
                        &action,
                        event.id,
                        &event.kind,
                        ExecutionEvent::SafeFailure,
                        "perp_terminal_fill_regressed",
                    )
                    .await?;
                    return Ok(());
                }
                if fill == 0 {
                    ObservationOutcome {
                        transition: Some(ExecutionEvent::PerpRejected),
                        complete: true,
                        result: event.payload.clone(),
                        next: None,
                    }
                } else {
                    ObservationOutcome {
                        transition: Some(ExecutionEvent::PerpFilled { filled_base: fill }),
                        complete: true,
                        result: event.payload.clone(),
                        next: Some(NextAction {
                            kind: ActionKind::SubmitSpot,
                            key: "hedge-spot".into(),
                            payload: json!({"filled_base": fill}),
                        }),
                    }
                }
            }
            _ => {
                self.store
                    .stop_action(
                        &action.id,
                        &self.worker_id,
                        &action.lease_token,
                        ActionStop::FailedSafe,
                        "unexpected_perp_event",
                        Some(ExecutionEvent::SafeFailure),
                        json!({"venue_event_id": event.id, "kind": event.kind}),
                    )
                    .await?;
                return Ok(());
            }
        };
        self.store
            .apply_venue_event(
                &action.id,
                &self.worker_id,
                &action.lease_token,
                event.id,
                outcome,
            )
            .await?;
        Ok(())
    }

    async fn submit_spot(&self, action: ClaimedAction) -> Result<(), WorkerError> {
        let filled_base = parse_fill(&action.payload)?;
        if let Some(submission) = saved_result::<RobinhoodSubmission>(&action.result, "submission")
        {
            self.complete_spot_submission(&action, filled_base, submission)
                .await?;
            return Ok(());
        }
        let now = now_ms()?;
        let recovered_request = recovered_robinhood_request(&action)?;
        let attempted = saved_result::<Value>(&action.result, "request").is_some()
            || recovered_request.is_some();
        if now > action.intent.deadline_ms && !attempted {
            self.schedule_unwind(&action, "spot_deadline_expired")
                .await?;
            return Ok(());
        }
        let Some(amounts) = action.intent.spot_amounts_for_fill(filled_base) else {
            self.schedule_unwind(&action, "spot_hedge_quantity_unrepresentable")
                .await?;
            return Ok(());
        };
        let expected = RobinhoodExecuteRequest {
            request_id: recovered_request
                .as_ref()
                .map_or_else(|| action.id.clone(), |request| request.request_id.clone()),
            replaces_request_id: None,
            intent: RobinhoodSpotIntent {
                id: action.intent.id.clone(),
                stock_token: action.intent.spot_token.clone(),
                side: "buy_spot".into(),
                amount_in: amounts.settlement_amount_in.to_string(),
                min_amount_out: amounts.minimum_spot_amount_out.to_string(),
                deadline: action.intent.deadline_ms.div_ceil(1_000),
                config_version: action.intent.spot_config_version,
            },
        };
        let request = self
            .persist_robinhood_request(&action, recovered_request, expected)
            .await?;
        match self.signers.execute_robinhood_spot(&request).await {
            Ok(submission) => {
                let result =
                    serde_json::to_value(&submission).map_err(|_| WorkerError::InvalidPayload)?;
                self.store
                    .record_action_result(
                        &action.id,
                        &self.worker_id,
                        &action.lease_token,
                        "submission",
                        result.clone(),
                    )
                    .await?;
                self.complete_spot_submission(&action, filled_base, submission)
                    .await?;
            }
            Err(SignerClientError::SignerRejected(409)) => {
                self.store
                    .continue_ambiguous_action(
                        &action.id,
                        &self.worker_id,
                        &action.lease_token,
                        "robinhood_request_conflict",
                        Some(ExecutionEvent::SpotSubmitted),
                        json!({"request_id": request.request_id}),
                        NextAction {
                            kind: ActionKind::ReconcileSpot,
                            key: "reconcile-spot".into(),
                            payload: json!({
                                "filled_base": filled_base,
                                "request_id": request.request_id,
                            }),
                        },
                    )
                    .await?;
            }
            Err(error) if deterministic_robinhood_rejection(&error) => {
                self.schedule_unwind(&action, "spot_submission_rejected")
                    .await?;
            }
            Err(_) => {
                self.retry(&action, "robinhood_signer_unavailable").await?;
            }
        }
        Ok(())
    }

    async fn complete_spot_submission(
        &self,
        action: &ClaimedAction,
        filled_base: u64,
        submission: RobinhoodSubmission,
    ) -> Result<(), StoreError> {
        let result = serde_json::to_value(&submission).map_err(|_| StoreError::InvalidAction)?;
        let transition = match action.saga.state {
            ExecutionState::PerpPartial | ExecutionState::PerpFilled => {
                Some(ExecutionEvent::SpotSubmitted)
            }
            ExecutionState::SpotSubmitted => None,
            _ => return Err(StoreError::InvalidAction),
        };
        self.store
            .complete_action(
                &action.id,
                &self.worker_id,
                &action.lease_token,
                transition,
                result,
                Some(NextAction {
                    kind: ActionKind::ReconcileSpot,
                    key: "reconcile-spot".into(),
                    payload: json!({
                        "filled_base": filled_base,
                        "request_id": submission.request_id,
                        "tx_hash": submission.tx_hash,
                    }),
                }),
            )
            .await?;
        Ok(())
    }

    async fn reconcile_spot(&self, action: ClaimedAction) -> Result<(), WorkerError> {
        let Some(event) = self.store.next_venue_event(&action).await? else {
            if now_ms()? > action.intent.reconciliation_deadline_ms {
                self.store.halt("spot_reconciliation_overdue").await?;
            }
            self.retry(&action, "awaiting_spot_event").await?;
            return Ok(());
        };
        let filled_base = parse_fill(&action.payload)?;
        let amounts = action
            .intent
            .spot_amounts_for_fill(filled_base)
            .ok_or(WorkerError::InvalidPayload)?;
        let observation = match spot_observation(&action, &event.payload, &action.intent.id) {
            Ok(observation) => observation,
            Err(_) => {
                self.reject_observation(
                    &action,
                    event.id,
                    &event.kind,
                    ExecutionEvent::SafeFailure,
                    "spot_observation_mismatch",
                )
                .await?;
                return Ok(());
            }
        };
        let outcome = match event.kind.as_str() {
            "spot_confirmed" => {
                let received = observation
                    .amount_out()
                    .ok_or(WorkerError::InvalidPayload)?;
                let matched = observation.amount_in() == Some(amounts.settlement_amount_in)
                    && received == amounts.target_spot_amount;
                ObservationOutcome {
                    transition: Some(if matched {
                        ExecutionEvent::SpotConfirmed {
                            received_raw: received,
                        }
                    } else {
                        ExecutionEvent::SpotMismatched {
                            received_raw: received,
                        }
                    }),
                    complete: true,
                    result: event.payload.clone(),
                    next: (!matched).then(|| NextAction {
                        kind: ActionKind::UnwindPerp,
                        key: "emergency-unwind-perp".into(),
                        payload: json!({"filled_base": filled_base}),
                    }),
                }
            }
            "spot_rejected" => ObservationOutcome {
                transition: Some(ExecutionEvent::SpotRejected),
                complete: true,
                result: event.payload.clone(),
                next: Some(NextAction {
                    kind: ActionKind::UnwindPerp,
                    key: "emergency-unwind-perp".into(),
                    payload: json!({"filled_base": filled_base}),
                }),
            },
            _ => {
                self.store
                    .stop_action(
                        &action.id,
                        &self.worker_id,
                        &action.lease_token,
                        ActionStop::FailedSafe,
                        "unexpected_spot_event",
                        Some(ExecutionEvent::SafeFailure),
                        json!({"venue_event_id": event.id, "kind": event.kind}),
                    )
                    .await?;
                return Ok(());
            }
        };
        self.store
            .apply_venue_event(
                &action.id,
                &self.worker_id,
                &action.lease_token,
                event.id,
                outcome,
            )
            .await?;
        Ok(())
    }

    async fn reconcile_unwind(&self, action: ClaimedAction) -> Result<(), WorkerError> {
        let Some(event) = self.store.next_venue_event(&action).await? else {
            if now_ms()? > unwind_reconciliation_deadline_ms(&action) {
                self.store
                    .halt("perp_unwind_reconciliation_overdue")
                    .await?;
            }
            self.retry(&action, "awaiting_unwind_event").await?;
            return Ok(());
        };
        let expected = parse_fill(&action.payload)?;
        let attempt = unwind_attempt(&action)?;
        let unwound_before = action
            .payload
            .get("unwound_before")
            .and_then(Value::as_u64)
            .unwrap_or(0);
        let observation = match perp_observation(&action, &event.payload, true) {
            Ok(observation) => observation,
            Err(_) => {
                self.reject_observation(
                    &action,
                    event.id,
                    &event.kind,
                    ExecutionEvent::PerpUnwindFailed {
                        unwound_base: action.saga.perp_unwound_base,
                    },
                    "unwind_observation_mismatch",
                )
                .await?;
                return Ok(());
            }
        };
        let filled = observation
            .filled_base()
            .ok_or(WorkerError::InvalidPayload)?;
        let total = unwound_before
            .checked_add(filled)
            .ok_or(WorkerError::InvalidPayload)?;
        if filled > expected
            || total > action.saga.perp_filled_base
            || total < action.saga.perp_unwound_base
        {
            self.reject_observation(
                &action,
                event.id,
                &event.kind,
                ExecutionEvent::Unhedged,
                "unwind_fill_mismatch",
            )
            .await?;
            return Ok(());
        }
        match event.kind.as_str() {
            "unwind_accepted" => {
                self.store
                    .apply_venue_event(
                        &action.id,
                        &self.worker_id,
                        &action.lease_token,
                        event.id,
                        ObservationOutcome {
                            transition: None,
                            complete: false,
                            result: event.payload,
                            next: None,
                        },
                    )
                    .await?;
            }
            "unwind_partial" => {
                let transition = (total > action.saga.perp_unwound_base
                    && total < action.saga.perp_filled_base)
                    .then_some(ExecutionEvent::PerpUnwindProgress {
                        unwound_base: total,
                    });
                self.store
                    .apply_venue_event(
                        &action.id,
                        &self.worker_id,
                        &action.lease_token,
                        event.id,
                        ObservationOutcome {
                            transition,
                            complete: false,
                            result: event.payload,
                            next: None,
                        },
                    )
                    .await?;
            }
            "unwind_filled" | "unwind_rejected" if total == action.saga.perp_filled_base => {
                let has_spot_inventory = action.saga.spot_received_raw > 0;
                self.store
                    .apply_venue_event(
                        &action.id,
                        &self.worker_id,
                        &action.lease_token,
                        event.id,
                        ObservationOutcome {
                            transition: Some(ExecutionEvent::PerpUnwindCompleted {
                                unwound_base: total,
                            }),
                            complete: true,
                            result: event.payload,
                            next: has_spot_inventory.then(|| NextAction {
                                kind: ActionKind::UnwindSpot,
                                key: "emergency-unwind-spot".into(),
                                payload: json!({
                                    "spot_amount": action.saga.spot_received_raw.to_string(),
                                    "exit_authority": action.payload.get("exit_authority"),
                                }),
                            }),
                        },
                    )
                    .await?;
            }
            "unwind_filled" | "unwind_rejected"
                if attempt + 1 < u16::from(action.intent.max_unwind_attempts)
                    && now_ms()? <= unwind_submission_deadline_ms(&action) =>
            {
                let next_attempt = attempt + 1;
                let transition = (total > action.saga.perp_unwound_base).then_some(
                    ExecutionEvent::PerpUnwindProgress {
                        unwound_base: total,
                    },
                );
                self.store
                    .apply_venue_event(
                        &action.id,
                        &self.worker_id,
                        &action.lease_token,
                        event.id,
                        ObservationOutcome {
                            transition,
                            complete: true,
                            result: event.payload,
                            next: Some(NextAction {
                                kind: ActionKind::UnwindPerp,
                                key: format!("emergency-unwind-perp-{next_attempt}"),
                                payload: json!({
                                    "filled_base": action.saga.perp_filled_base - total,
                                    "unwind_attempt": next_attempt,
                                    "unwound_before": total,
                                    "exit_authority": action.payload.get("exit_authority"),
                                }),
                            }),
                        },
                    )
                    .await?;
            }
            "unwind_filled" | "unwind_rejected" => {
                self.store
                    .stop_action(
                        &action.id,
                        &self.worker_id,
                        &action.lease_token,
                        ActionStop::FailedSafe,
                        "unwind_incomplete",
                        Some(ExecutionEvent::PerpUnwindFailed {
                            unwound_base: total,
                        }),
                        json!({"venue_event_id": event.id, "payload": event.payload}),
                    )
                    .await?;
            }
            _ => {
                self.store
                    .stop_action(
                        &action.id,
                        &self.worker_id,
                        &action.lease_token,
                        ActionStop::FailedSafe,
                        "unexpected_unwind_event",
                        Some(ExecutionEvent::Unhedged),
                        json!({"venue_event_id": event.id, "kind": event.kind}),
                    )
                    .await?;
            }
        }
        Ok(())
    }

    async fn unwind_spot(&self, action: ClaimedAction) -> Result<(), WorkerError> {
        let spot_amount = parse_u128_field(&action.payload, "spot_amount")?;
        if let Some(submission) = saved_result::<RobinhoodSubmission>(&action.result, "submission")
        {
            self.complete_unwind_spot_submission(&action, spot_amount, submission)
                .await?;
            return Ok(());
        }
        let now = now_ms()?;
        let recovered_request = recovered_robinhood_request(&action)?;
        let attempted = saved_result::<Value>(&action.result, "request").is_some()
            || recovered_request.is_some();
        let send_authorized = lighter_send_authorized(&action.result);
        let submission_deadline_ms = unwind_submission_deadline_ms(&action);
        if now > submission_deadline_ms && !attempted && !send_authorized {
            self.store
                .stop_action(
                    &action.id,
                    &self.worker_id,
                    &action.lease_token,
                    ActionStop::FailedSafe,
                    "spot_unwind_expired",
                    Some(ExecutionEvent::Unhedged),
                    json!({"spot_amount": spot_amount.to_string()}),
                )
                .await?;
            return Ok(());
        }
        if !attempted && !send_authorized {
            match self
                .store
                .authorize_unwind_send(&action.id, &self.worker_id, &action.lease_token, now)
                .await
            {
                Ok(()) => {}
                Err(StoreError::MarketAuthorityUnavailable) => {
                    self.store
                        .stop_action(
                            &action.id,
                            &self.worker_id,
                            &action.lease_token,
                            ActionStop::FailedSafe,
                            "spot_exit_authority_expired",
                            None,
                            json!({"stage": "spot_unwind_send"}),
                        )
                        .await?;
                    return Ok(());
                }
                Err(error) => return Err(error.into()),
            }
        }
        let minimum = unwind_minimum_out(&action, spot_amount)?;
        let expected = RobinhoodExecuteRequest {
            request_id: recovered_request
                .as_ref()
                .map_or_else(|| action.id.clone(), |request| request.request_id.clone()),
            replaces_request_id: None,
            intent: RobinhoodSpotIntent {
                id: action.intent.spot_unwind_intent_id.clone(),
                stock_token: action.intent.spot_token.clone(),
                side: "sell_spot".into(),
                amount_in: spot_amount.to_string(),
                min_amount_out: minimum.to_string(),
                deadline: submission_deadline_ms.div_ceil(1_000),
                config_version: action.intent.spot_config_version,
            },
        };
        let request = self
            .persist_robinhood_request(&action, recovered_request, expected)
            .await?;
        match self.signers.execute_robinhood_spot(&request).await {
            Ok(submission) => {
                let result =
                    serde_json::to_value(&submission).map_err(|_| WorkerError::InvalidPayload)?;
                self.store
                    .record_action_result(
                        &action.id,
                        &self.worker_id,
                        &action.lease_token,
                        "submission",
                        result.clone(),
                    )
                    .await?;
                self.complete_unwind_spot_submission(&action, spot_amount, submission)
                    .await?;
            }
            Err(SignerClientError::SignerRejected(409)) => {
                self.store
                    .continue_ambiguous_action(
                        &action.id,
                        &self.worker_id,
                        &action.lease_token,
                        "robinhood_unwind_request_conflict",
                        None,
                        json!({"request_id": request.request_id}),
                        NextAction {
                            kind: ActionKind::ReconcileUnwindSpot,
                            key: "reconcile-unwind-spot".into(),
                            payload: json!({
                                "spot_amount": spot_amount.to_string(),
                                "request_id": request.request_id,
                                "exit_authority": action.payload.get("exit_authority"),
                            }),
                        },
                    )
                    .await?;
            }
            Err(error) if deterministic_robinhood_rejection(&error) => {
                self.store
                    .stop_action(
                        &action.id,
                        &self.worker_id,
                        &action.lease_token,
                        ActionStop::FailedSafe,
                        "spot_unwind_rejected",
                        Some(ExecutionEvent::Unhedged),
                        json!({"spot_amount": spot_amount.to_string()}),
                    )
                    .await?;
            }
            Err(_) => {
                self.retry(&action, "spot_unwind_signer_unavailable")
                    .await?;
            }
        }
        Ok(())
    }

    async fn complete_unwind_spot_submission(
        &self,
        action: &ClaimedAction,
        spot_amount: u128,
        submission: RobinhoodSubmission,
    ) -> Result<(), StoreError> {
        let result = serde_json::to_value(&submission).map_err(|_| StoreError::InvalidAction)?;
        self.store
            .complete_action(
                &action.id,
                &self.worker_id,
                &action.lease_token,
                None,
                result,
                Some(NextAction {
                    kind: ActionKind::ReconcileUnwindSpot,
                    key: "reconcile-unwind-spot".into(),
                    payload: json!({
                        "spot_amount": spot_amount.to_string(),
                        "request_id": submission.request_id,
                        "tx_hash": submission.tx_hash,
                        "exit_authority": action.payload.get("exit_authority"),
                    }),
                }),
            )
            .await?;
        Ok(())
    }

    async fn persist_robinhood_request(
        &self,
        action: &ClaimedAction,
        recovered: Option<RobinhoodExecuteRequest>,
        expected: RobinhoodExecuteRequest,
    ) -> Result<RobinhoodExecuteRequest, WorkerError> {
        let stored = action
            .result
            .as_ref()
            .and_then(|value| value.get("request"))
            .cloned()
            .map(serde_json::from_value)
            .transpose()
            .map_err(|_| WorkerError::InvalidPayload)?;
        let stored_exists = stored.is_some();
        if stored.as_ref().is_some_and(|request| request != &expected)
            || recovered
                .as_ref()
                .is_some_and(|request| request != &expected)
        {
            return Err(WorkerError::InvalidPayload);
        }
        let request = stored.or(recovered).unwrap_or_else(|| expected.clone());
        if !stored_exists {
            self.store
                .record_action_result(
                    &action.id,
                    &self.worker_id,
                    &action.lease_token,
                    "request",
                    serde_json::to_value(&request).map_err(|_| WorkerError::InvalidPayload)?,
                )
                .await?;
        }
        Ok(request)
    }

    async fn overfilled_perp(
        &self,
        action: &ClaimedAction,
        venue_event_id: i64,
        filled_base: u64,
    ) -> Result<(), WorkerError> {
        self.store.halt("perp_fill_limit_exceeded").await?;
        self.store
            .apply_venue_event(
                &action.id,
                &self.worker_id,
                &action.lease_token,
                venue_event_id,
                ObservationOutcome {
                    transition: Some(ExecutionEvent::PerpOverfilled { filled_base }),
                    complete: true,
                    result: json!({"filled_base": filled_base.to_string()}),
                    next: Some(NextAction {
                        kind: ActionKind::UnwindPerp,
                        key: "emergency-unwind-perp".into(),
                        payload: json!({"filled_base": filled_base}),
                    }),
                },
            )
            .await?;
        Ok(())
    }

    async fn reject_observation(
        &self,
        action: &ClaimedAction,
        venue_event_id: i64,
        venue_event_kind: &str,
        transition: ExecutionEvent,
        error_code: &str,
    ) -> Result<(), WorkerError> {
        self.store
            .stop_action(
                &action.id,
                &self.worker_id,
                &action.lease_token,
                ActionStop::FailedSafe,
                error_code,
                Some(transition),
                json!({
                    "venue_event_id": venue_event_id,
                    "venue_event_kind": venue_event_kind,
                }),
            )
            .await?;
        Ok(())
    }

    async fn reconcile_unwind_spot(&self, action: ClaimedAction) -> Result<(), WorkerError> {
        let Some(event) = self.store.next_venue_event(&action).await? else {
            if now_ms()? > unwind_reconciliation_deadline_ms(&action) {
                self.store
                    .halt("spot_unwind_reconciliation_overdue")
                    .await?;
            }
            self.retry(&action, "awaiting_spot_unwind_event").await?;
            return Ok(());
        };
        let spot_amount = parse_u128_field(&action.payload, "spot_amount")?;
        let minimum = unwind_minimum_out(&action, spot_amount)?;
        let observation = match spot_observation(
            &action,
            &event.payload,
            &action.intent.spot_unwind_intent_id,
        ) {
            Ok(observation) => observation,
            Err(_) => {
                self.reject_observation(
                    &action,
                    event.id,
                    &event.kind,
                    ExecutionEvent::Unhedged,
                    "spot_unwind_observation_mismatch",
                )
                .await?;
                return Ok(());
            }
        };
        match event.kind.as_str() {
            "spot_unwind_confirmed" => {
                let conserved = observation.amount_in() == Some(spot_amount)
                    && observation
                        .amount_out()
                        .is_some_and(|amount| amount >= minimum);
                if !conserved {
                    self.store
                        .stop_action(
                            &action.id,
                            &self.worker_id,
                            &action.lease_token,
                            ActionStop::FailedSafe,
                            "spot_unwind_conservation_failed",
                            Some(ExecutionEvent::Unhedged),
                            json!({"venue_event_id": event.id}),
                        )
                        .await?;
                    return Ok(());
                }
                self.store
                    .apply_venue_event(
                        &action.id,
                        &self.worker_id,
                        &action.lease_token,
                        event.id,
                        ObservationOutcome {
                            transition: Some(ExecutionEvent::Closed),
                            complete: true,
                            result: event.payload,
                            next: None,
                        },
                    )
                    .await?;
            }
            "spot_unwind_rejected" => {
                self.store
                    .stop_action(
                        &action.id,
                        &self.worker_id,
                        &action.lease_token,
                        ActionStop::FailedSafe,
                        "spot_unwind_rejected",
                        Some(ExecutionEvent::Unhedged),
                        json!({"venue_event_id": event.id}),
                    )
                    .await?;
            }
            _ => {
                self.store
                    .stop_action(
                        &action.id,
                        &self.worker_id,
                        &action.lease_token,
                        ActionStop::FailedSafe,
                        "unexpected_spot_unwind_event",
                        Some(ExecutionEvent::Unhedged),
                        json!({"venue_event_id": event.id, "kind": event.kind}),
                    )
                    .await?;
            }
        }
        Ok(())
    }

    async fn schedule_unwind(
        &self,
        action: &ClaimedAction,
        reason: &str,
    ) -> Result<(), StoreError> {
        self.store.halt(reason).await?;
        let filled_base = action
            .payload
            .get("filled_base")
            .and_then(Value::as_u64)
            .ok_or(StoreError::InvalidAction)?;
        self.store
            .complete_action(
                &action.id,
                &self.worker_id,
                &action.lease_token,
                Some(ExecutionEvent::UnwindStarted),
                json!({"reason": reason}),
                Some(NextAction {
                    kind: ActionKind::UnwindPerp,
                    key: "emergency-unwind-perp".into(),
                    payload: json!({"filled_base": filled_base}),
                }),
            )
            .await?;
        Ok(())
    }

    async fn retry(&self, action: &ClaimedAction, code: &str) -> Result<(), StoreError> {
        let exponent = action.attempts.saturating_sub(1).min(4);
        let delay = Duration::from_secs(1u64 << exponent);
        self.store
            .reschedule_action(
                &action.id,
                &self.worker_id,
                &action.lease_token,
                delay,
                code,
            )
            .await
    }
}

fn is_poison_error(error: &WorkerError) -> bool {
    matches!(
        error,
        WorkerError::InvalidPayload
            | WorkerError::Store(
                StoreError::InvalidAction | StoreError::InvalidSaga | StoreError::Transition(_)
            )
    )
}

fn unwind_submission_deadline_ms(action: &ClaimedAction) -> u64 {
    exit_authority(action)
        .map(|authority| authority.submission_deadline_ms)
        .unwrap_or(0)
}

fn unwind_reconciliation_deadline_ms(action: &ClaimedAction) -> u64 {
    exit_authority(action)
        .map(|authority| authority.reconciliation_deadline_ms)
        .unwrap_or(0)
}

fn exit_authority(action: &ClaimedAction) -> Result<ExitAuthority, WorkerError> {
    let authority = action
        .payload
        .get("exit_authority")
        .cloned()
        .ok_or(WorkerError::InvalidPayload)?;
    let authority: ExitAuthority =
        serde_json::from_value(authority).map_err(|_| WorkerError::InvalidPayload)?;
    let spot_amount = authority
        .spot_amount_in
        .parse::<u128>()
        .map_err(|_| WorkerError::InvalidPayload)?;
    let minimum = authority
        .minimum_unwind_settlement_out
        .parse::<u128>()
        .map_err(|_| WorkerError::InvalidPayload)?;
    if authority.quote_source_session.is_empty()
        || authority.quote_source_event_id.is_empty()
        || authority.perp_mark_price == 0
        || authority.perp_unwind_price < authority.perp_mark_price
        || authority.submission_deadline_ms > authority.quote_expires_at_ms
        || authority.reconciliation_deadline_ms <= authority.submission_deadline_ms
        || (spot_amount == 0) != (minimum == 0)
        || spot_amount != action.saga.spot_received_raw
    {
        return Err(WorkerError::InvalidPayload);
    }
    Ok(authority)
}

fn unwind_minimum_out(action: &ClaimedAction, spot_amount: u128) -> Result<u128, WorkerError> {
    let authority = exit_authority(action)?;
    if authority
        .spot_amount_in
        .parse::<u128>()
        .ok()
        .filter(|amount| *amount == spot_amount)
        .is_none()
    {
        return Err(WorkerError::InvalidPayload);
    }
    authority
        .minimum_unwind_settlement_out
        .parse::<u128>()
        .ok()
        .filter(|minimum| *minimum > 0)
        .ok_or(WorkerError::InvalidPayload)
}

fn lighter_request(
    action: &ClaimedAction,
    nonce: i64,
    unwind: bool,
) -> Result<LighterCreateOrderRequest, WorkerError> {
    let filled_base = if unwind {
        parse_fill(&action.payload)?
    } else {
        action.intent.perp_base_amount
    };
    let client_order_index = if unwind {
        unwind_client_order_index(action)?
    } else {
        action.intent.client_order_index
    };
    Ok(LighterCreateOrderRequest {
        intent_id: action.intent.id.clone(),
        market_index: i16::try_from(action.intent.lighter_market_index)
            .map_err(|_| WorkerError::InvalidPayload)?,
        client_order_index: i64::try_from(client_order_index)
            .map_err(|_| WorkerError::InvalidPayload)?,
        base_amount: i64::try_from(filled_base).map_err(|_| WorkerError::InvalidPayload)?,
        price: if unwind {
            exit_authority(action)?.perp_unwind_price
        } else {
            action.intent.perp_limit_price
        },
        is_ask: !unwind,
        order_type: 0,
        time_in_force: 0,
        reduce_only: unwind,
        trigger_price: 0,
        order_expiry_ms: 0,
        transaction: LighterTransactionOptions {
            nonce,
            expires_at_ms: i64::try_from(if unwind {
                unwind_submission_deadline_ms(action)
            } else {
                action.intent.perp_order_expiry_ms
            })
            .map_err(|_| WorkerError::InvalidPayload)?,
        },
    })
}

fn unwind_attempt(action: &ClaimedAction) -> Result<u16, WorkerError> {
    let operator_recovery = action
        .payload
        .get("operator_recovery")
        .and_then(Value::as_bool)
        .unwrap_or(false);
    let attempt = action
        .payload
        .get("unwind_attempt")
        .and_then(Value::as_u64)
        .unwrap_or_else(|| u64::from(action.intent.max_unwind_attempts));
    let attempt = u16::try_from(attempt).map_err(|_| WorkerError::InvalidPayload)?;
    if !operator_recovery && attempt >= u16::from(action.intent.max_unwind_attempts) {
        return Err(WorkerError::InvalidPayload);
    }
    Ok(attempt)
}

fn unwind_client_order_index(action: &ClaimedAction) -> Result<u64, WorkerError> {
    if let Some(value) = action
        .payload
        .get("client_order_index")
        .and_then(Value::as_u64)
    {
        if value == 0 {
            return Err(WorkerError::InvalidPayload);
        }
        let operator_recovery = action
            .payload
            .get("operator_recovery")
            .and_then(Value::as_bool)
            .unwrap_or(false);
        if !operator_recovery {
            let attempt = unwind_attempt(action)?;
            let expected = action
                .intent
                .unwind_client_order_index
                .checked_add(u64::from(attempt))
                .ok_or(WorkerError::InvalidPayload)?;
            if value != expected {
                return Err(WorkerError::InvalidPayload);
            }
        }
        return Ok(value);
    }
    let attempt = unwind_attempt(action)?;
    if attempt >= u16::from(action.intent.max_unwind_attempts) {
        return Err(WorkerError::InvalidPayload);
    }
    action
        .intent
        .unwind_client_order_index
        .checked_add(u64::from(attempt))
        .ok_or(WorkerError::InvalidPayload)
}

fn parse_fill(value: &Value) -> Result<u64, WorkerError> {
    let fill = serde_json::from_value::<FilledBase>(value.clone())
        .map_err(|_| WorkerError::InvalidPayload)?
        .filled_base;
    if fill == 0 {
        return Err(WorkerError::InvalidPayload);
    }
    Ok(fill)
}

fn parse_u128_field(value: &Value, field: &str) -> Result<u128, WorkerError> {
    value
        .get(field)
        .and_then(Value::as_str)
        .and_then(|value| value.parse().ok())
        .filter(|value| *value > 0)
        .ok_or(WorkerError::InvalidPayload)
}

fn perp_observation(
    action: &ClaimedAction,
    payload: &Value,
    unwind: bool,
) -> Result<PerpObservation, WorkerError> {
    let observation = serde_json::from_value::<PerpObservation>(payload.clone())
        .map_err(|_| WorkerError::InvalidPayload)?;
    let expected_order_index = if unwind {
        unwind_client_order_index(action)?
    } else {
        action.intent.client_order_index
    };
    let expected_tx_hash = action
        .payload
        .get("tx_hash")
        .and_then(Value::as_str)
        .ok_or(WorkerError::InvalidPayload)?;
    let expected_order_id = action
        .result
        .as_ref()
        .and_then(|value| value.get("order_id"))
        .and_then(Value::as_str);
    if observation.client_order_index != expected_order_index
        || observation.market_index != action.intent.lighter_market_index
        || observation.is_ask == unwind
        || observation.reduce_only != unwind
        || !observation
            .transaction_hash
            .eq_ignore_ascii_case(expected_tx_hash)
        || expected_order_id.is_some_and(|order_id| order_id != observation.order_id)
    {
        return Err(WorkerError::InvalidPayload);
    }
    Ok(observation)
}

fn fill_within_cap(
    action: &ClaimedAction,
    observation: &PerpObservation,
    filled_base: u64,
) -> bool {
    filled_base <= action.intent.perp_base_amount
        && observation.average_price().is_some_and(|price| {
            action
                .intent
                .perp_notional_for_fill(filled_base, price)
                .is_some_and(|notional| notional <= action.intent.perp_notional_micros)
        })
}

fn spot_observation(
    action: &ClaimedAction,
    payload: &Value,
    expected_intent_id: &str,
) -> Result<SpotObservation, WorkerError> {
    let observation = serde_json::from_value::<SpotObservation>(payload.clone())
        .map_err(|_| WorkerError::InvalidPayload)?;
    if observation.spot_intent_id != expected_intent_id
        || observation.config_version != action.intent.spot_config_version
        || !valid_transaction_hash(&observation.tx_hash)
    {
        return Err(WorkerError::InvalidPayload);
    }
    Ok(observation)
}

fn valid_transaction_hash(value: &str) -> bool {
    value.len() == 66
        && value.starts_with("0x")
        && value[2..]
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
        && value[2..].bytes().any(|byte| byte != b'0')
}

fn saved_result<T>(result: &Option<Value>, field: &str) -> Option<T>
where
    T: for<'de> Deserialize<'de>,
{
    result
        .as_ref()?
        .get(field)
        .cloned()
        .and_then(|value| serde_json::from_value(value).ok())
}

fn lighter_send_authorized(result: &Option<Value>) -> bool {
    saved_result::<Value>(result, "send_authorized").is_some()
}

fn recovered_robinhood_request(
    action: &ClaimedAction,
) -> Result<Option<RobinhoodExecuteRequest>, WorkerError> {
    action
        .payload
        .get("recovery_request")
        .cloned()
        .map(serde_json::from_value)
        .transpose()
        .map_err(|_| WorkerError::InvalidPayload)
}

fn now_ms() -> Result<u64, WorkerError> {
    let millis = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_err(|_| WorkerError::InvalidPayload)?
        .as_millis();
    u64::try_from(millis).map_err(|_| WorkerError::InvalidPayload)
}

fn deterministic_robinhood_rejection(error: &SignerClientError) -> bool {
    matches!(
        error,
        SignerClientError::Clock
            | SignerClientError::Encoding
            | SignerClientError::RobinhoodJournalRejected(_)
            | SignerClientError::SignerRejected(400 | 401 | 403 | 422)
    )
}

fn retryable_lighter_signer_error(error: &SignerClientError) -> bool {
    matches!(
        error,
        SignerClientError::SignerTransport
            | SignerClientError::Clock
            | SignerClientError::SignerRejected(408 | 425 | 429 | 500..=599)
    )
}

#[cfg(test)]
mod tests {
    use super::*;
    use execution::{FrozenEvidence, PairIntent, PerpSide, SpotSide};

    #[test]
    fn entry_and_unwind_orders_are_opposite_and_typed() {
        let entry = action(ActionKind::SubmitPerp, json!({"nonce": 3}));
        let request = lighter_request(&entry, 3, false).unwrap();
        assert!(request.is_ask);
        assert!(!request.reduce_only);
        assert_eq!(request.time_in_force, 0);
        assert_eq!(request.order_expiry_ms, 0);

        let unwind = action(
            ActionKind::UnwindPerp,
            json!({
                "filled_base": 500_000,
                "nonce": 4,
                "unwind_attempt": 2,
                "exit_authority": exit_authority_payload(),
            }),
        );
        let request = lighter_request(&unwind, 4, true).unwrap();
        assert!(!request.is_ask);
        assert!(request.reduce_only);
        assert_eq!(request.base_amount, 500_000);
        assert_eq!(request.client_order_index, 4);
        assert_eq!(request.order_expiry_ms, 0);
    }

    #[test]
    fn operator_recovery_uses_its_durable_client_order_index() {
        let recovery = action(
            ActionKind::UnwindPerp,
            json!({
                "filled_base": 500_000,
                "nonce": 4,
                "unwind_attempt": 3,
                "operator_recovery": true,
                "client_order_index": 8_000_000_000_000_000_001_u64,
                "exit_authority": exit_authority_payload(),
            }),
        );
        let request = lighter_request(&recovery, 4, true).unwrap();
        assert_eq!(request.client_order_index, 8_000_000_000_000_000_001_i64);
        assert!(request.reduce_only);
    }

    #[test]
    fn observations_are_bound_to_the_submitted_transaction() {
        let action = action(
            ActionKind::ReconcilePerp,
            json!({
                "tx_hash": "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
            }),
        );
        let valid = json!({
            "order_id": "order-1",
            "transaction_hash": "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            "client_order_index": 1,
            "market_index": 101,
            "is_ask": true,
            "reduce_only": false,
            "filled_base": "500000",
            "average_price": "25000"
        });
        assert!(perp_observation(&action, &valid, false).is_ok());

        let mut wrong = valid;
        wrong["transaction_hash"] =
            json!("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb");
        assert!(perp_observation(&action, &wrong, false).is_err());
    }

    #[test]
    fn durable_payload_and_transition_corruption_are_poison_errors() {
        assert!(is_poison_error(&WorkerError::InvalidPayload));
        assert!(is_poison_error(&WorkerError::Store(
            StoreError::InvalidSaga
        )));
        assert!(is_poison_error(&WorkerError::Store(
            StoreError::Transition(execution::SagaError::InvalidTransition {
                state: execution::ExecutionState::Created,
            })
        )));
    }

    #[test]
    fn robinhood_conflict_requires_reconciliation() {
        assert!(!deterministic_robinhood_rejection(
            &SignerClientError::SignerRejected(409)
        ));
        assert!(deterministic_robinhood_rejection(
            &SignerClientError::RobinhoodJournalRejected("reverted".into())
        ));
        assert!(!deterministic_robinhood_rejection(
            &SignerClientError::SignerRejected(429)
        ));
        assert!(!deterministic_robinhood_rejection(
            &SignerClientError::SignerRejected(500)
        ));
        assert!(!deterministic_robinhood_rejection(
            &SignerClientError::InvalidSignerResponse
        ));
    }

    #[test]
    fn spot_observation_accepts_a_canonical_replacement_family_hash() {
        let action = action(
            ActionKind::ReconcileSpot,
            json!({
                "tx_hash": "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
            }),
        );
        let observation = json!({
            "spot_intent_id": action.intent.id,
            "tx_hash": "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
            "block_hash": "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
            "block_number": 10,
            "finality": "ethereum_final",
            "config_version": 1,
            "amount_in": "25000000",
            "amount_out": "2000000"
        });
        assert!(spot_observation(&action, &observation, &action.intent.id).is_ok());
    }

    #[test]
    fn transient_lighter_signer_failures_are_retryable() {
        for status in [408, 425, 429, 500, 503, 599] {
            assert!(retryable_lighter_signer_error(
                &SignerClientError::SignerRejected(status)
            ));
        }
        for status in [400, 401, 403, 409, 422] {
            assert!(!retryable_lighter_signer_error(
                &SignerClientError::SignerRejected(status)
            ));
        }
    }

    #[test]
    fn a_signed_lighter_transaction_is_not_live_without_send_authorization() {
        let tx_hash = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
        let signed_only = Some(json!({"signed": {"tx_hash": tx_hash}}));
        let authorized = Some(json!({
            "signed": {"tx_hash": tx_hash},
            "send_authorized": {"control_version": 4}
        }));
        assert!(!lighter_send_authorized(&signed_only));
        assert!(lighter_send_authorized(&authorized));
    }

    fn action(kind: ActionKind, payload: Value) -> ClaimedAction {
        let intent = PairIntent {
            id: "0x1111111111111111111111111111111111111111111111111111111111111111".into(),
            spot_unwind_intent_id:
                "0x2222222222222222222222222222222222222222222222222222222222222222".into(),
            symbol: "NVDA".into(),
            spot_token: "0x0000000000000000000000000000000000000001".into(),
            lighter_market_index: 101,
            spot_side: SpotSide::Buy,
            perp_side: PerpSide::Short,
            spot_notional_micros: 25_000_000,
            perp_notional_micros: 25_000_000,
            nav_micros: 10_000_000_000,
            raw_spot_amount: 2_000_000,
            settlement_amount_in: 25_000_000,
            minimum_spot_amount_out: 1_990_000,
            minimum_unwind_settlement_out: 24_000_000,
            spot_decimals: 6,
            spot_config_version: 1,
            perp_base_amount: 1_000_000,
            perp_base_decimals: 6,
            perp_price_decimals: 3,
            perp_limit_price: 25_000,
            client_order_index: 1,
            perp_unwind_price: 30_000,
            unwind_client_order_index: 2,
            max_unwind_attempts: 3,
            perp_order_expiry_ms: 1_800_000_300_000,
            emergency_deadline_ms: 1_800_000_600_000,
            reconciliation_deadline_ms: 1_800_086_400_000,
            leverage_micros: 1_000_000,
            created_at_ms: 1_800_000_000_000,
            deadline_ms: 1_800_000_001_500,
            evidence: FrozenEvidence {
                dataset_manifest:
                    "0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd".into(),
                strategy_version: "strategy".into(),
                market_manifest:
                    "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa".into(),
                quote_block_hash:
                    "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb".into(),
                quote_received_at_ms: 1_800_000_000_000,
                quote_expires_at_ms: 1_800_000_001_500,
                ui_multiplier_e18: 500_000_000_000_000_000,
                perp_mark_price: 25_000,
                estimated_total_cost_micros: 10_000,
            },
        };
        ClaimedAction {
            id: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa".into(),
            lease_token: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb".into(),
            saga: execution::ExecutionSaga::new(&intent.id).unwrap(),
            intent,
            kind,
            payload,
            result: None,
            attempts: 1,
            control_version: 1,
        }
    }

    fn exit_authority_payload() -> Value {
        json!({
            "quote_source_session": "session-1",
            "quote_source_event_id": "quote-1",
            "quote_expires_at_ms": 1_800_000_300_000u64,
            "perp_mark_price": 25_000,
            "perp_unwind_price": 30_000,
            "spot_amount_in": "0",
            "minimum_unwind_settlement_out": "0",
            "submission_deadline_ms": 1_800_000_300_000u64,
            "reconciliation_deadline_ms": 1_800_086_400_000u64,
        })
    }
}
