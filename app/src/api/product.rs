use crate::api::error::ApiError;
use crate::auth::require_user;
use crate::evm::abi;
use crate::lighter_provisioner::{
    ConfirmLink, ConfirmRevocation, LighterProvisionerError, PrepareLink, RevocationBinding,
};
use crate::product::{
    ActivityPage, AgentCommandInput, AgentCreateInput, AgentExecutionStatus, AgentStatusInput,
    Amount, ConfirmVaultInput, ConfirmedVault, DashboardSnapshot, LighterConfirmInput,
    LighterLinkRequestInput, LighterRevocationConfirmInput, MetricInput, OpportunitySnapshot,
    PreferencesInput, RobinhoodConfirmInput, TransactionCall, TransactionPlan, VaultSnapshot,
    WalletBalanceSnapshot, LIVE_STRATEGY_VERSION,
};
use crate::product_store::normalize_address;
use crate::robinhood_provisioner::{ConfirmGraph, PrepareGraph};
use crate::state::AppState;
use actix_web::{web, HttpRequest, HttpResponse};
use num_bigint::BigUint;
use serde::Deserialize;
use serde_json::Value;
use sha3::{Digest, Keccak256};
use uuid::Uuid;

pub async fn me(req: HttpRequest, state: web::Data<AppState>) -> Result<HttpResponse, ApiError> {
    let auth = require_user(&req, &state)?;
    ensure_database(&state)?;
    let response = state
        .product_store
        .me(&auth.did)
        .await
        .map_err(ApiError::internal)?;
    Ok(HttpResponse::Ok().json(response))
}

pub async fn sync_wallets(
    req: HttpRequest,
    state: web::Data<AppState>,
) -> Result<HttpResponse, ApiError> {
    let auth = require_user(&req, &state)?;
    ensure_database(&state)?;
    let identity = state
        .privy
        .identity(&auth.did)
        .await
        .map_err(|error| ApiError::ServiceUnavailable(error.to_string()))?;
    let response = state
        .product_store
        .sync_identity(&auth.did, &identity, state.config.app_chain_id)
        .await
        .map_err(|error| {
            if error.to_string().contains("linked to another account") {
                ApiError::Conflict("This wallet is linked to another account.".to_string())
            } else {
                ApiError::internal(error)
            }
        })?;
    Ok(HttpResponse::Ok().json(response))
}

pub async fn update_preferences(
    req: HttpRequest,
    state: web::Data<AppState>,
    input: web::Json<PreferencesInput>,
) -> Result<HttpResponse, ApiError> {
    let auth = require_user(&req, &state)?;
    ensure_database(&state)?;
    if !matches!(input.display_currency.as_str(), "USD" | "EUR" | "GBP") {
        return Err(ApiError::BadRequest(
            "Display currency must be USD, EUR, or GBP.".to_string(),
        ));
    }
    let response = state
        .product_store
        .update_preferences(&auth.did, &input)
        .await
        .map_err(|error| ApiError::BadRequest(error.to_string()))?;
    Ok(HttpResponse::Ok().json(response))
}

pub async fn prepare_vault(
    req: HttpRequest,
    state: web::Data<AppState>,
) -> Result<HttpResponse, ApiError> {
    let auth = require_user(&req, &state)?;
    ensure_database(&state)?;
    ensure_contracts(&state)?;
    let me = state
        .product_store
        .me(&auth.did)
        .await
        .map_err(ApiError::internal)?;
    if !me.user.has_recovery {
        return Err(ApiError::BadRequest(
            "Add an email or passkey before creating a vault.".to_string(),
        ));
    }
    if me.vault.is_some() {
        return Err(ApiError::Conflict(
            "This account already has a personal vault.".to_string(),
        ));
    }
    let smart_account = me
        .smart_account
        .ok_or_else(|| ApiError::BadRequest("Embedded wallet is not ready.".to_string()))?
        .address;

    let existing = read_address(
        &state,
        &state.config.personal_vault_factory,
        &abi::call_address("vaultOf(address)", &smart_account)
            .map_err(|error| ApiError::BadRequest(error.to_string()))?,
    )
    .await?;
    if !is_zero_address(&existing) {
        return Err(ApiError::Conflict(
            "The vault exists onchain. Resume the pending confirmation.".to_string(),
        ));
    }

    let expected_vault = read_address(
        &state,
        &state.config.personal_vault_factory,
        &abi::call_address("predictVault(address)", &smart_account)
            .map_err(|error| ApiError::BadRequest(error.to_string()))?,
    )
    .await?;
    let already_claimed = read_bool(
        &state,
        &state.config.test_faucet_address,
        &abi::call_address("claimed(address)", &smart_account)
            .map_err(|error| ApiError::BadRequest(error.to_string()))?,
    )
    .await?;

    let mut calls = Vec::with_capacity(4);
    if !already_claimed {
        calls.push(TransactionCall {
            to: normalize_address(&state.config.test_faucet_address)
                .map_err(|error| ApiError::ServiceUnavailable(error.to_string()))?,
            data: abi::call_no_args("claim()"),
            value: "0".to_string(),
        });
    }
    calls.push(TransactionCall {
        to: normalize_address(&state.config.test_asset_address)
            .map_err(|error| ApiError::ServiceUnavailable(error.to_string()))?,
        data: abi::call_address_u256(
            "approve(address,uint256)",
            &expected_vault,
            &state.config.test_claim_amount,
        )
        .map_err(|error| ApiError::BadRequest(error.to_string()))?,
        value: "0".to_string(),
    });
    calls.push(TransactionCall {
        to: normalize_address(&state.config.personal_vault_factory)
            .map_err(|error| ApiError::ServiceUnavailable(error.to_string()))?,
        data: abi::call_no_args("createVault()"),
        value: "0".to_string(),
    });
    calls.push(TransactionCall {
        to: expected_vault.clone(),
        data: abi::call_u256("deposit(uint256)", &state.config.test_claim_amount)
            .map_err(|error| ApiError::BadRequest(error.to_string()))?,
        value: "0".to_string(),
    });

    Ok(HttpResponse::Ok().json(TransactionPlan {
        chain_id: state.config.app_chain_id,
        smart_account,
        expected_vault,
        calls,
    }))
}

pub async fn confirm_vault(
    req: HttpRequest,
    state: web::Data<AppState>,
    input: web::Json<ConfirmVaultInput>,
) -> Result<HttpResponse, ApiError> {
    let auth = require_user(&req, &state)?;
    ensure_database(&state)?;
    ensure_contracts(&state)?;
    if !is_hex_identifier(&input.call_id) {
        return Err(ApiError::BadRequest(
            "Invalid operation identifier.".to_string(),
        ));
    }

    let me = state
        .product_store
        .me(&auth.did)
        .await
        .map_err(ApiError::internal)?;
    if let Some(vault) = me.vault {
        if vault.call_id == input.call_id {
            return Ok(HttpResponse::Ok().json(vault));
        }
        return Err(ApiError::Conflict(
            "This account already has a different personal vault.".to_string(),
        ));
    }
    let owner = me
        .smart_account
        .ok_or_else(|| ApiError::BadRequest("Embedded wallet is not ready.".to_string()))?
        .address;
    let confirmed = verify_vault_creation(&state, &owner, &input.call_id).await?;
    let record = state
        .product_store
        .confirm_vault(&auth.did, &confirmed)
        .await
        .map_err(ApiError::internal)?;
    Ok(HttpResponse::Ok().json(record))
}

pub async fn dashboard(
    req: HttpRequest,
    state: web::Data<AppState>,
) -> Result<HttpResponse, ApiError> {
    let auth = require_user(&req, &state)?;
    ensure_database(&state)?;
    let me = state
        .product_store
        .me(&auth.did)
        .await
        .map_err(ApiError::internal)?;
    let agent = state
        .product_store
        .agent_snapshot(me.user.id)
        .await
        .map_err(ApiError::internal)?;
    let activity = state
        .product_store
        .activity(&auth.did, None, 12)
        .await
        .map_err(ApiError::internal)?
        .items;
    let ready = state.config.product_contracts_ready();
    let available_raw = if ready {
        if let Some(account) = &me.smart_account {
            token_balance(&state, &state.config.test_asset_address, &account.address).await?
        } else {
            "0".to_string()
        }
    } else {
        "0".to_string()
    };
    let vault_snapshot = if let Some(vault) = me.vault.clone() {
        let balance = token_balance(&state, &vault.asset_address, &vault.vault_address).await?;
        let halted =
            read_bool(&state, &vault.guard_address, &abi::call_no_args("halted()")).await?;
        let remaining = read_u256(
            &state,
            &vault.guard_address,
            &abi::call_no_args("remaining()"),
        )
        .await?;
        Some(VaultSnapshot {
            record: vault,
            balance: amount(&state, balance),
            halted,
            remaining_capacity: amount(&state, remaining),
        })
    } else {
        None
    };
    let deployed_raw = vault_snapshot
        .as_ref()
        .map(|vault| vault.balance.raw.clone())
        .unwrap_or_else(|| "0".to_string());
    let mut wallets = Vec::with_capacity(me.wallets.len());
    let mut linked_raw = "0".to_string();
    for wallet in &me.wallets {
        let raw = if ready {
            token_balance(&state, &state.config.test_asset_address, &wallet.address).await?
        } else {
            "0".to_string()
        };
        linked_raw = abi::sum_decimal(&linked_raw, &raw).map_err(ApiError::internal)?;
        wallets.push(WalletBalanceSnapshot {
            wallet: wallet.clone(),
            balance: amount(&state, raw),
        });
    }
    let total_raw = abi::sum_decimal(&linked_raw, &deployed_raw).map_err(ApiError::internal)?;
    let opportunities = state
        .store
        .recent_observations(12)
        .await
        .into_iter()
        .map(|record| OpportunitySnapshot {
            symbol: record.symbol,
            basis_bps: format!("{:.4}", record.basis_bps),
            liquidity: format!("{:.4}", record.liquidity),
            observed_at: record.observed_at,
        })
        .collect();

    Ok(HttpResponse::Ok().json(DashboardSnapshot {
        environment: "robinhood-mainnet".to_string(),
        as_of: chrono::Utc::now(),
        infrastructure_ready: ready,
        agent,
        total_value: amount(&state, total_raw),
        available_balance: amount(&state, available_raw),
        deployed_capital: amount(&state, deployed_raw),
        pnl: None,
        smart_account: me.smart_account,
        vault: vault_snapshot,
        positions: Vec::new(),
        opportunities,
        activity,
        wallets,
    }))
}

pub async fn launch_agent(
    req: HttpRequest,
    state: web::Data<AppState>,
    body: web::Bytes,
) -> Result<HttpResponse, ApiError> {
    let auth = require_user(&req, &state)?;
    ensure_database(&state)?;
    let input: AgentCreateInput = serde_json::from_slice(&body)
        .map_err(|_| ApiError::BadRequest("Invalid live agent request.".to_string()))?;
    if input.strategy_version != LIVE_STRATEGY_VERSION {
        return Err(ApiError::BadRequest(
            "Only basis-aapl-v1 is available for live execution.".to_string(),
        ));
    }
    let agent = state
        .product_store
        .create_live_agent(&auth.did, &input.strategy_version)
        .await
        .map_err(|error| ApiError::Conflict(error.to_string()))?;
    Ok(HttpResponse::Created().json(agent))
}

pub async fn update_agent_status(
    req: HttpRequest,
    state: web::Data<AppState>,
    path: web::Path<Uuid>,
    input: web::Json<AgentStatusInput>,
) -> Result<HttpResponse, ApiError> {
    let auth = require_user(&req, &state)?;
    ensure_database(&state)?;
    if !matches!(input.status.as_str(), "running" | "paused") {
        return Err(ApiError::BadRequest(
            "Agent status must be running or paused.".to_string(),
        ));
    }
    let agent = state
        .product_store
        .set_agent_status(&auth.did, path.into_inner(), &input.status)
        .await
        .map_err(|error| ApiError::BadRequest(error.to_string()))?;
    Ok(HttpResponse::Ok().json(agent))
}

pub async fn create_execution_account(
    req: HttpRequest,
    state: web::Data<AppState>,
    path: web::Path<Uuid>,
) -> Result<HttpResponse, ApiError> {
    let auth = require_user(&req, &state)?;
    ensure_database(&state)?;
    let account = state
        .product_store
        .create_execution_account(&auth.did, path.into_inner())
        .await
        .map_err(|error| ApiError::BadRequest(error.to_string()))?;
    Ok(HttpResponse::Accepted().json(account))
}

pub async fn lighter_link_request(
    req: HttpRequest,
    state: web::Data<AppState>,
    path: web::Path<Uuid>,
    input: web::Json<LighterLinkRequestInput>,
) -> Result<HttpResponse, ApiError> {
    let auth = require_user(&req, &state)?;
    ensure_database(&state)?;
    if !state.lighter_provisioner.is_enabled() {
        return Err(ApiError::ServiceUnavailable(
            "Lighter provisioning is not enabled.".to_string(),
        ));
    }
    let agent_id = path.into_inner();
    let binding = state
        .product_store
        .request_execution_binding(&auth.did, agent_id, "lighter", &input.owner_address)
        .await
        .map_err(|error| ApiError::BadRequest(error.to_string()))?;
    if binding.status != "provisioning" {
        return Ok(HttpResponse::Ok().json(binding));
    }
    let account = state
        .product_store
        .execution_account(&auth.did, agent_id)
        .await
        .map_err(ApiError::internal)?;
    let api_key_index = u8::try_from(state.config.lighter_api_key_index)
        .ok()
        .filter(|index| (4..=254).contains(index))
        .ok_or_else(|| {
            ApiError::ServiceUnavailable("Lighter API key policy is invalid.".to_string())
        })?;
    let link = state
        .lighter_provisioner
        .prepare(&PrepareLink {
            execution_account_id: account.id,
            owner_address: &binding.owner_address,
            api_key_index,
        })
        .await
        .map_err(lighter_provisioner_error)?;
    let binding = state
        .product_store
        .apply_lighter_link(&auth.did, agent_id, binding.request_id, &link)
        .await
        .map_err(|error| ApiError::Conflict(error.to_string()))?;
    Ok(HttpResponse::Created().json(binding))
}

pub async fn lighter_confirm(
    req: HttpRequest,
    state: web::Data<AppState>,
    path: web::Path<Uuid>,
    input: web::Json<LighterConfirmInput>,
) -> Result<HttpResponse, ApiError> {
    let auth = require_user(&req, &state)?;
    ensure_database(&state)?;
    if !state.lighter_provisioner.is_enabled() {
        return Err(ApiError::ServiceUnavailable(
            "Lighter provisioning is not enabled.".to_string(),
        ));
    }
    let agent_id = path.into_inner();
    let binding = state
        .product_store
        .execution_binding(&auth.did, agent_id, "lighter", input.request_id)
        .await
        .map_err(|error| ApiError::BadRequest(error.to_string()))?;
    if binding.provider_request_id != Some(input.link_id) {
        return Err(ApiError::Conflict(
            "Lighter link does not match this binding request.".to_string(),
        ));
    }
    let account = state
        .product_store
        .execution_account(&auth.did, agent_id)
        .await
        .map_err(ApiError::internal)?;
    let link = state
        .lighter_provisioner
        .confirm(&ConfirmLink {
            execution_account_id: account.id,
            link_id: input.link_id,
            l1_signature: &input.l1_signature,
        })
        .await
        .map_err(lighter_provisioner_error)?;
    let binding = state
        .product_store
        .apply_lighter_link(&auth.did, agent_id, input.request_id, &link)
        .await
        .map_err(|error| ApiError::Conflict(error.to_string()))?;
    if binding.status == "linked" {
        Ok(HttpResponse::Ok().json(binding))
    } else {
        Ok(HttpResponse::Accepted().json(binding))
    }
}

pub async fn lighter_revocation(
    req: HttpRequest,
    state: web::Data<AppState>,
    path: web::Path<Uuid>,
) -> Result<HttpResponse, ApiError> {
    let auth = require_user(&req, &state)?;
    ensure_database(&state)?;
    if !state.lighter_provisioner.is_enabled() {
        return Err(ApiError::ServiceUnavailable(
            "Lighter provisioning is not enabled.".to_string(),
        ));
    }
    let agent_id = path.into_inner();
    let command = state
        .product_store
        .pending_agent_command(&auth.did, agent_id)
        .await
        .map_err(ApiError::internal)?
        .filter(|command| command.command == "close")
        .ok_or_else(|| ApiError::Conflict("Agent has no pending close command.".to_string()))?;
    let identity = state
        .product_store
        .lighter_binding_identity(&auth.did, agent_id)
        .await
        .map_err(ApiError::internal)?;
    if identity.execution_account_id != command.execution_account_id {
        return Err(ApiError::Conflict(
            "Close command does not match the execution account.".to_string(),
        ));
    }
    let binding = revocation_binding(identity)?;
    let revocation = state
        .lighter_provisioner
        .revocation_status(&binding)
        .await
        .map_err(lighter_provisioner_error)?;
    if revocation.status == "revoked" {
        Ok(HttpResponse::Ok().json(revocation))
    } else {
        Ok(HttpResponse::Accepted().json(revocation))
    }
}

pub async fn lighter_revocation_confirm(
    req: HttpRequest,
    state: web::Data<AppState>,
    path: web::Path<Uuid>,
    input: web::Json<LighterRevocationConfirmInput>,
) -> Result<HttpResponse, ApiError> {
    let auth = require_user(&req, &state)?;
    ensure_database(&state)?;
    if !state.lighter_provisioner.is_enabled() {
        return Err(ApiError::ServiceUnavailable(
            "Lighter provisioning is not enabled.".to_string(),
        ));
    }
    let agent_id = path.into_inner();
    let command = state
        .product_store
        .pending_agent_command(&auth.did, agent_id)
        .await
        .map_err(ApiError::internal)?
        .filter(|command| command.command == "close")
        .ok_or_else(|| ApiError::Conflict("Agent has no pending close command.".to_string()))?;
    let identity = state
        .product_store
        .lighter_binding_identity(&auth.did, agent_id)
        .await
        .map_err(ApiError::internal)?;
    if identity.execution_account_id != command.execution_account_id {
        return Err(ApiError::Conflict(
            "Close command does not match the execution account.".to_string(),
        ));
    }
    let binding = revocation_binding(identity)?;
    let revocation = state
        .lighter_provisioner
        .confirm_revocation(
            &ConfirmRevocation {
                execution_account_id: binding.execution_account_id,
                revocation_id: input.revocation_id,
                l1_signature: &input.l1_signature,
            },
            &binding,
        )
        .await
        .map_err(lighter_provisioner_error)?;
    if revocation.status == "revoked" {
        Ok(HttpResponse::Ok().json(revocation))
    } else {
        Ok(HttpResponse::Accepted().json(revocation))
    }
}

fn revocation_binding(
    identity: crate::product::LighterBindingIdentity,
) -> Result<RevocationBinding, ApiError> {
    let api_key_index = u8::try_from(identity.api_key_index)
        .map_err(|_| ApiError::Conflict("Linked Lighter API key index is invalid.".to_string()))?;
    Ok(RevocationBinding {
        execution_account_id: identity.execution_account_id,
        owner_address: identity.owner_address,
        account_index: identity.account_index,
        api_key_index,
    })
}

fn lighter_provisioner_error(error: anyhow::Error) -> ApiError {
    match error.downcast_ref::<LighterProvisionerError>() {
        Some(LighterProvisionerError::Rejected(message)) => ApiError::BadRequest(message.clone()),
        Some(LighterProvisionerError::Conflict(message)) => ApiError::Conflict(message.clone()),
        Some(LighterProvisionerError::Unavailable) | None => {
            ApiError::ServiceUnavailable(error.to_string())
        }
    }
}

pub async fn robinhood_prepare(
    req: HttpRequest,
    state: web::Data<AppState>,
    path: web::Path<Uuid>,
) -> Result<HttpResponse, ApiError> {
    let auth = require_user(&req, &state)?;
    ensure_database(&state)?;
    if !state.robinhood_provisioner.is_enabled() {
        return Err(ApiError::ServiceUnavailable(
            "Robinhood graph provisioning is not enabled.".to_string(),
        ));
    }
    let agent_id = path.into_inner();
    let me = state
        .product_store
        .me(&auth.did)
        .await
        .map_err(ApiError::internal)?;
    let owner = me
        .wallets
        .iter()
        .find(|wallet| wallet.is_primary)
        .ok_or_else(|| {
            ApiError::BadRequest("Select a primary execution wallet first.".to_string())
        })?;
    let binding = state
        .product_store
        .request_execution_binding(&auth.did, agent_id, "robinhood", &owner.address)
        .await
        .map_err(|error| ApiError::BadRequest(error.to_string()))?;
    let account = state
        .product_store
        .execution_account(&auth.did, agent_id)
        .await
        .map_err(ApiError::internal)?;
    let graph = state
        .robinhood_provisioner
        .prepare(&PrepareGraph {
            execution_account_id: account.id,
            owner_address: &binding.owner_address,
        })
        .await
        .map_err(|error| ApiError::ServiceUnavailable(error.to_string()))?;
    let binding = state
        .product_store
        .apply_robinhood_prepare(&auth.did, agent_id, binding.request_id, &graph)
        .await
        .map_err(|error| ApiError::Conflict(error.to_string()))?;
    Ok(HttpResponse::Ok().json(binding))
}

pub async fn robinhood_confirm(
    req: HttpRequest,
    state: web::Data<AppState>,
    path: web::Path<Uuid>,
    input: web::Json<RobinhoodConfirmInput>,
) -> Result<HttpResponse, ApiError> {
    let auth = require_user(&req, &state)?;
    ensure_database(&state)?;
    if !state.robinhood_provisioner.is_enabled() {
        return Err(ApiError::ServiceUnavailable(
            "Robinhood graph provisioning is not enabled.".to_string(),
        ));
    }
    if !is_transaction_hash(&input.transaction_hash) {
        return Err(ApiError::BadRequest(
            "Robinhood deployment transaction is invalid.".to_string(),
        ));
    }
    let agent_id = path.into_inner();
    let binding = state
        .product_store
        .execution_binding(&auth.did, agent_id, "robinhood", input.request_id)
        .await
        .map_err(|error| ApiError::BadRequest(error.to_string()))?;
    let account = state
        .product_store
        .execution_account(&auth.did, agent_id)
        .await
        .map_err(ApiError::internal)?;
    if binding.provider_request_id != Some(account.id) {
        return Err(ApiError::Conflict(
            "Robinhood deployment does not match this execution account.".to_string(),
        ));
    }
    let graph = state
        .robinhood_provisioner
        .confirm(&ConfirmGraph {
            execution_account_id: account.id,
            transaction_hash: &input.transaction_hash,
        })
        .await
        .map_err(|error| ApiError::ServiceUnavailable(error.to_string()))?;
    let binding = state
        .product_store
        .apply_robinhood_confirmation(
            &auth.did,
            agent_id,
            input.request_id,
            &input.transaction_hash,
            &graph,
        )
        .await
        .map_err(|error| ApiError::Conflict(error.to_string()))?;
    Ok(HttpResponse::Ok().json(binding))
}

pub async fn agent_readiness(
    req: HttpRequest,
    state: web::Data<AppState>,
    path: web::Path<Uuid>,
) -> Result<HttpResponse, ApiError> {
    let auth = require_user(&req, &state)?;
    ensure_database(&state)?;
    let readiness = state
        .product_store
        .agent_readiness(&auth.did, path.into_inner())
        .await
        .map_err(|error| ApiError::BadRequest(error.to_string()))?;
    Ok(HttpResponse::Ok().json(readiness))
}

pub async fn agent_execution(
    req: HttpRequest,
    state: web::Data<AppState>,
    path: web::Path<Uuid>,
) -> Result<HttpResponse, ApiError> {
    let auth = require_user(&req, &state)?;
    ensure_database(&state)?;
    let account = state
        .product_store
        .execution_account(&auth.did, path.into_inner())
        .await
        .map_err(|error| ApiError::BadRequest(error.to_string()))?;
    let execution = state
        .coordinator_registration
        .execution(&account)
        .await
        .map_err(|error| ApiError::ServiceUnavailable(error.to_string()))?;
    Ok(HttpResponse::Ok().json(AgentExecutionStatus {
        execution_account_id: execution.execution_account_id,
        agent_id: execution.agent_id,
        strategy_version: execution.strategy_version,
        strategy_manifest_sha256: execution.strategy_manifest_sha256,
        account_status: execution.account_status,
        control_mode: execution.control_mode,
        active: execution.active,
        flat: execution.flat,
        intent_id: execution.intent_id,
        symbol: execution.symbol,
        state: execution.state,
        spot_amount_raw: execution.spot_amount_raw,
        spot_decimals: execution.spot_decimals,
        perp_open_base: execution.perp_open_base,
        perp_base_decimals: execution.perp_base_decimals,
        spot_notional_micros: execution.spot_notional_micros,
        perp_notional_micros: execution.perp_notional_micros,
        lighter_order_id: execution.lighter_order_id,
        lighter_transaction_hash: execution.lighter_transaction_hash,
        robinhood_transaction_hash: execution.robinhood_transaction_hash,
        lighter_unwind_order_id: execution.lighter_unwind_order_id,
        lighter_unwind_transaction_hash: execution.lighter_unwind_transaction_hash,
        robinhood_unwind_transaction_hash: execution.robinhood_unwind_transaction_hash,
        updated_at_ms: execution.updated_at_ms,
    }))
}

pub async fn create_agent_command(
    req: HttpRequest,
    state: web::Data<AppState>,
    path: web::Path<Uuid>,
    input: web::Json<AgentCommandInput>,
) -> Result<HttpResponse, ApiError> {
    let auth = require_user(&req, &state)?;
    ensure_database(&state)?;
    let idempotency_key = req
        .headers()
        .get("Idempotency-Key")
        .and_then(|value| value.to_str().ok())
        .map(str::trim)
        .filter(|value| {
            !value.is_empty()
                && value.len() <= 128
                && value.bytes().all(|byte| byte.is_ascii_graphic())
        })
        .ok_or_else(|| {
            ApiError::BadRequest("A valid Idempotency-Key header is required.".to_string())
        })?;
    if !matches!(
        input.command.as_str(),
        "launch" | "pause" | "resume" | "close" | "withdraw"
    ) {
        return Err(ApiError::BadRequest(
            "Unsupported agent command.".to_string(),
        ));
    }
    let command = state
        .product_store
        .create_agent_command(
            &auth.did,
            path.into_inner(),
            idempotency_key,
            &input.command,
        )
        .await
        .map_err(|error| ApiError::BadRequest(error.to_string()))?;
    if command.status == "rejected" {
        Ok(HttpResponse::Conflict().json(command))
    } else if command.status == "completed" {
        Ok(HttpResponse::Ok().json(command))
    } else {
        Ok(HttpResponse::Accepted().json(command))
    }
}

pub async fn agent_command(
    req: HttpRequest,
    state: web::Data<AppState>,
    path: web::Path<(Uuid, Uuid)>,
) -> Result<HttpResponse, ApiError> {
    let auth = require_user(&req, &state)?;
    ensure_database(&state)?;
    let (agent_id, command_id) = path.into_inner();
    let command = state
        .product_store
        .agent_command(&auth.did, agent_id, command_id)
        .await
        .map_err(|error| ApiError::BadRequest(error.to_string()))?;
    Ok(HttpResponse::Ok().json(command))
}

pub async fn pending_agent_command(
    req: HttpRequest,
    state: web::Data<AppState>,
    path: web::Path<Uuid>,
) -> Result<HttpResponse, ApiError> {
    let auth = require_user(&req, &state)?;
    ensure_database(&state)?;
    let command = state
        .product_store
        .pending_agent_command(&auth.did, path.into_inner())
        .await
        .map_err(|error| ApiError::BadRequest(error.to_string()))?;
    Ok(HttpResponse::Ok().json(command))
}

#[derive(Deserialize)]
pub struct ActivityQuery {
    cursor: Option<Uuid>,
    limit: Option<usize>,
}

pub async fn activity(
    req: HttpRequest,
    state: web::Data<AppState>,
    query: web::Query<ActivityQuery>,
) -> Result<HttpResponse, ApiError> {
    let auth = require_user(&req, &state)?;
    ensure_database(&state)?;
    let page: ActivityPage = state
        .product_store
        .activity(&auth.did, query.cursor, query.limit.unwrap_or(50).min(100))
        .await
        .map_err(|error| ApiError::BadRequest(error.to_string()))?;
    Ok(HttpResponse::Ok().json(page))
}

pub async fn metric(
    req: HttpRequest,
    state: web::Data<AppState>,
    input: web::Json<MetricInput>,
) -> Result<HttpResponse, ApiError> {
    let auth = require_user(&req, &state)?;
    ensure_database(&state)?;
    const ALLOWED: &[&str] = &[
        "dashboard_load",
        "login_ready",
        "wallet_sync",
        "onboarding_started",
        "user_operation_included",
        "onboarding_completed",
        "onboarding_confirmation_delayed",
    ];
    if !ALLOWED.contains(&input.name.as_str()) {
        return Err(ApiError::BadRequest(
            "Unsupported product metric.".to_string(),
        ));
    }
    if input
        .duration_ms
        .is_some_and(|duration| duration > 3_600_000)
    {
        return Err(ApiError::BadRequest(
            "Metric duration is out of range.".to_string(),
        ));
    }
    let status = input.status.as_deref();
    if status.is_some_and(|value| {
        value.len() > 32
            || !value
                .bytes()
                .all(|byte| byte.is_ascii_alphanumeric() || byte == b'_')
    }) {
        return Err(ApiError::BadRequest(
            "Metric status is invalid.".to_string(),
        ));
    }
    state
        .product_store
        .record_metric(&auth.did, &input.name, input.duration_ms, status)
        .await
        .map_err(ApiError::internal)?;
    Ok(HttpResponse::NoContent().finish())
}

fn ensure_database(state: &AppState) -> Result<(), ApiError> {
    if state.product_store.is_enabled() {
        Ok(())
    } else {
        Err(ApiError::ServiceUnavailable(
            "Application database is not configured.".to_string(),
        ))
    }
}

fn ensure_contracts(state: &AppState) -> Result<(), ApiError> {
    if state.config.product_contracts_ready() {
        Ok(())
    } else {
        Err(ApiError::ServiceUnavailable(
            "Vault onboarding is not configured.".to_string(),
        ))
    }
}

fn amount(state: &AppState, raw: String) -> Amount {
    Amount {
        raw,
        decimals: state.config.test_asset_decimals,
        symbol: state.config.test_asset_symbol.clone(),
    }
}

async fn token_balance(state: &AppState, asset: &str, account: &str) -> Result<String, ApiError> {
    let data = abi::call_address("balanceOf(address)", account)
        .map_err(|error| ApiError::BadRequest(error.to_string()))?;
    read_u256(state, asset, &data).await
}

async fn read_address(state: &AppState, contract: &str, data: &str) -> Result<String, ApiError> {
    let value = state
        .product_rpc
        .eth_call(contract, data)
        .await
        .map_err(|error| ApiError::ServiceUnavailable(error.to_string()))?;
    abi::decode_address(&value).map_err(|error| ApiError::ServiceUnavailable(error.to_string()))
}

async fn read_bool(state: &AppState, contract: &str, data: &str) -> Result<bool, ApiError> {
    let value = state
        .product_rpc
        .eth_call(contract, data)
        .await
        .map_err(|error| ApiError::ServiceUnavailable(error.to_string()))?;
    abi::decode_bool(&value).map_err(|error| ApiError::ServiceUnavailable(error.to_string()))
}

async fn read_u256(state: &AppState, contract: &str, data: &str) -> Result<String, ApiError> {
    let value = state
        .product_rpc
        .eth_call(contract, data)
        .await
        .map_err(|error| ApiError::ServiceUnavailable(error.to_string()))?;
    abi::decode_u256(&value).map_err(|error| ApiError::ServiceUnavailable(error.to_string()))
}

async fn verify_vault_creation(
    state: &AppState,
    owner: &str,
    call_id: &str,
) -> Result<ConfirmedVault, ApiError> {
    let status = state
        .wallet_rpc
        .wallet_get_calls_status(call_id)
        .await
        .map_err(|error| ApiError::ServiceUnavailable(error.to_string()))?;
    ensure_call_succeeded(&status)?;
    let transaction_hash = status
        .get("receipts")
        .and_then(Value::as_array)
        .and_then(|receipts| receipts.first())
        .and_then(|receipt| receipt.get("transactionHash"))
        .and_then(Value::as_str)
        .ok_or_else(|| ApiError::Conflict("Operation is still waiting for inclusion.".to_string()))?
        .to_string();
    let receipt = state
        .product_rpc
        .eth_get_transaction_receipt(&transaction_hash)
        .await
        .map_err(|error| ApiError::Conflict(error.to_string()))?;
    if receipt.get("status").and_then(Value::as_str) != Some("0x1") {
        return Err(ApiError::BadRequest(
            "Vault creation failed onchain.".to_string(),
        ));
    }

    let factory = normalize_address(&state.config.personal_vault_factory)
        .map_err(|error| ApiError::ServiceUnavailable(error.to_string()))?;
    let event_topic = format!(
        "0x{}",
        hex::encode(Keccak256::digest(
            b"VaultCreated(address,address,address,address,address,uint64)"
        ))
    );
    let log = receipt
        .get("logs")
        .and_then(Value::as_array)
        .and_then(|logs| {
            logs.iter().find(|log| {
                let address_matches = log
                    .get("address")
                    .and_then(Value::as_str)
                    .and_then(|address| normalize_address(address).ok())
                    .is_some_and(|address| address == factory);
                let topic_matches = log
                    .get("topics")
                    .and_then(Value::as_array)
                    .and_then(|topics| topics.first())
                    .and_then(Value::as_str)
                    .is_some_and(|topic| topic.eq_ignore_ascii_case(&event_topic));
                address_matches && topic_matches
            })
        })
        .ok_or_else(|| ApiError::BadRequest("Vault creation event was not found.".to_string()))?;
    let topics = log
        .get("topics")
        .and_then(Value::as_array)
        .ok_or_else(|| ApiError::BadRequest("Vault creation event is malformed.".to_string()))?;
    let event_owner = topics
        .get(1)
        .and_then(Value::as_str)
        .ok_or_else(|| ApiError::BadRequest("Vault owner topic is missing.".to_string()))
        .and_then(|value| abi::decode_address(value).map_err(ApiError::internal))?;
    if !event_owner.eq_ignore_ascii_case(owner) {
        return Err(ApiError::BadRequest(
            "Vault owner does not match the account.".to_string(),
        ));
    }
    let vault_address = topics
        .get(2)
        .and_then(Value::as_str)
        .ok_or_else(|| ApiError::BadRequest("Vault address topic is missing.".to_string()))
        .and_then(|value| abi::decode_address(value).map_err(ApiError::internal))?;
    let data = log
        .get("data")
        .and_then(Value::as_str)
        .ok_or_else(|| ApiError::BadRequest("Vault creation data is missing.".to_string()))?;
    let guard_address = decode_event_address(data, 0)?;
    let anchor_address = decode_event_address(data, 1)?;
    let asset_address = decode_event_address(data, 2)?;
    let version = BigUint::parse_bytes(
        abi::word(data, 3).map_err(ApiError::internal)?.as_bytes(),
        16,
    )
    .and_then(|value| value.to_u64_digits().first().copied())
    .ok_or_else(|| ApiError::BadRequest("Factory version is invalid.".to_string()))?;
    if version != 1 {
        return Err(ApiError::BadRequest(
            "Factory version is not supported.".to_string(),
        ));
    }
    let expected_asset = normalize_address(&state.config.test_asset_address)
        .map_err(|error| ApiError::ServiceUnavailable(error.to_string()))?;
    if asset_address != expected_asset {
        return Err(ApiError::BadRequest(
            "Vault asset does not match configuration.".to_string(),
        ));
    }

    verify_address_getter(state, &vault_address, "owner()", owner).await?;
    verify_address_getter(state, &vault_address, "asset()", &asset_address).await?;
    verify_address_getter(state, &vault_address, "guard()", &guard_address).await?;
    verify_address_getter(
        state,
        &vault_address,
        "attestationAnchor()",
        &anchor_address,
    )
    .await?;

    Ok(ConfirmedVault {
        chain_id: state.config.app_chain_id as i64,
        factory_version: version as i64,
        asset_address,
        vault_address,
        guard_address,
        anchor_address,
        call_id: call_id.to_string(),
        transaction_hash,
        block_number: parse_hex_i64(
            receipt
                .get("blockNumber")
                .and_then(Value::as_str)
                .unwrap_or("0x0"),
        )?,
        log_index: parse_hex_i64(log.get("logIndex").and_then(Value::as_str).unwrap_or("0x0"))?,
    })
}

fn ensure_call_succeeded(status: &Value) -> Result<(), ApiError> {
    let value = status.get("status").unwrap_or(&Value::Null);
    if value.as_str().is_some_and(|status| {
        status.eq_ignore_ascii_case("failure") || status.eq_ignore_ascii_case("failed")
    }) {
        return Err(ApiError::BadRequest(
            "Vault creation operation failed.".to_string(),
        ));
    }
    if value.as_u64().is_some_and(|status| status >= 400) {
        return Err(ApiError::BadRequest(
            "Vault creation operation failed.".to_string(),
        ));
    }
    Ok(())
}

fn decode_event_address(data: &str, index: usize) -> Result<String, ApiError> {
    let word = abi::word(data, index).map_err(ApiError::internal)?;
    abi::decode_address(&format!("0x{word}"))
        .map_err(|error| ApiError::BadRequest(error.to_string()))
}

async fn verify_address_getter(
    state: &AppState,
    contract: &str,
    signature: &str,
    expected: &str,
) -> Result<(), ApiError> {
    let actual = read_address(state, contract, &abi::call_no_args(signature)).await?;
    if actual.eq_ignore_ascii_case(expected) {
        Ok(())
    } else {
        Err(ApiError::BadRequest(format!(
            "Onchain {signature} does not match the confirmed vault."
        )))
    }
}

fn parse_hex_i64(value: &str) -> Result<i64, ApiError> {
    i64::from_str_radix(value.trim_start_matches("0x"), 16)
        .map_err(|_| ApiError::BadRequest("Invalid receipt number.".to_string()))
}

fn is_zero_address(value: &str) -> bool {
    value.eq_ignore_ascii_case("0x0000000000000000000000000000000000000000")
}

fn is_hex_identifier(value: &str) -> bool {
    let Some(value) = value.strip_prefix("0x") else {
        return false;
    };
    (8..=510).contains(&value.len())
        && value.len() % 2 == 0
        && value.bytes().all(|b| b.is_ascii_hexdigit())
}

fn is_transaction_hash(value: &str) -> bool {
    value
        .strip_prefix("0x")
        .is_some_and(|value| value.len() == 64 && value.bytes().all(|b| b.is_ascii_hexdigit()))
        && !value[2..].bytes().all(|byte| byte == b'0')
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn validates_operation_identifiers() {
        assert!(is_hex_identifier("0x12345678"));
        assert!(!is_hex_identifier("12345678"));
        assert!(!is_hex_identifier("0x123"));
    }

    #[test]
    fn detects_failed_call_status() {
        assert!(ensure_call_succeeded(&serde_json::json!({"status": "failure"})).is_err());
        assert!(ensure_call_succeeded(&serde_json::json!({"status": "success"})).is_ok());
        assert!(ensure_call_succeeded(&serde_json::json!({"status": 400})).is_err());
        assert!(ensure_call_succeeded(&serde_json::json!({"status": 200})).is_ok());
    }
}
