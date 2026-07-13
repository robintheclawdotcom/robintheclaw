package publisher

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AccountSource interface {
	List(context.Context) (AccountDiscovery, error)
	Close()
}

type AccountDiscovery struct {
	Accounts    []AccountBinding
	RejectedIDs []string
}

type PGAccountSource struct {
	coordinator *pgxpool.Pool
	robinhood   *pgxpool.Pool
	journal     *pgxpool.Pool
	marketID    uint16
	minimums    accountMinimums
}

type accountMinimums struct {
	collateral string
	settlement string
	ownerGas   string
	signerGas  string
}

type registeredAccount struct {
	id              string
	lighterIndex    uint64
	apiKeyIndex     uint8
	owner           string
	vault           string
	signer          string
	policyActive    bool
	strategyVersion string
}

type coordinatorPolicyState struct {
	globalMode       sql.NullString
	strategyMode     sql.NullString
	accountMode      sql.NullString
	strategyManifest sql.NullString
	accountManifest  sql.NullString
	venueApproved    sql.NullBool
	oracleHealthy    sql.NullBool
	sequencerHealthy sql.NullBool
	reconciled       sql.NullBool
	exitReady        sql.NullBool
	alertingReady    sql.NullBool
	rotationReady    sql.NullBool
	lighterMarketID  sql.NullInt64
}

func (value coordinatorPolicyState) Active(expectedManifest string, expectedMarketID uint16) bool {
	return value.globalMode.Valid && value.globalMode.String == "ACTIVE" &&
		value.strategyMode.Valid && value.strategyMode.String == "ACTIVE" &&
		value.accountMode.Valid && value.accountMode.String == "ACTIVE" &&
		value.strategyManifest.Valid && value.strategyManifest.String == expectedManifest &&
		value.accountManifest.Valid && value.accountManifest.String == expectedManifest &&
		value.venueApproved.Valid && value.venueApproved.Bool &&
		value.oracleHealthy.Valid && value.oracleHealthy.Bool &&
		value.sequencerHealthy.Valid && value.sequencerHealthy.Bool &&
		value.reconciled.Valid && value.reconciled.Bool &&
		value.exitReady.Valid && value.exitReady.Bool &&
		value.alertingReady.Valid && value.alertingReady.Bool &&
		value.rotationReady.Valid && value.rotationReady.Bool &&
		value.lighterMarketID.Valid && value.lighterMarketID.Int64 == int64(expectedMarketID)
}

func NewPGAccountSource(ctx context.Context, config Config) (*PGAccountSource, error) {
	coordinator, err := openReadOnlyPool(ctx, config.CoordinatorDatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("open coordinator account source: %w", err)
	}
	robinhood, err := openReadOnlyPool(ctx, config.RobinhoodDatabaseURL)
	if err != nil {
		coordinator.Close()
		return nil, fmt.Errorf("open Robinhood account source: %w", err)
	}
	journal, err := openReadOnlyPool(ctx, config.RobinhoodJournalDatabaseURL)
	if err != nil {
		coordinator.Close()
		robinhood.Close()
		return nil, fmt.Errorf("open Robinhood journal source: %w", err)
	}
	return &PGAccountSource{
		coordinator: coordinator,
		robinhood:   robinhood,
		journal:     journal,
		marketID:    config.LighterMarketID,
		minimums: accountMinimums{
			collateral: config.MinimumCollateralRaw,
			settlement: config.MinimumSettlementRaw,
			ownerGas:   config.MinimumOwnerGasRaw,
			signerGas:  config.MinimumSignerGasRaw,
		},
	}, nil
}

func openReadOnlyPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("invalid database URL")
	}
	config.MaxConns = 2
	config.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		if _, err := connection.Exec(ctx, "SET default_transaction_read_only = on"); err != nil {
			return err
		}
		_, err := connection.Exec(ctx, "SET statement_timeout = '3500ms'")
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, errors.New("initialize read-only database pool")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.New("connect read-only database pool")
	}
	return pool, nil
}

func (value *PGAccountSource) Close() {
	value.coordinator.Close()
	value.robinhood.Close()
	value.journal.Close()
}

func (value *PGAccountSource) List(ctx context.Context) (AccountDiscovery, error) {
	registered, err := value.registered(ctx)
	if err != nil {
		return AccountDiscovery{}, err
	}
	if len(registered) == 0 {
		return AccountDiscovery{}, nil
	}
	seenLighter := make(map[uint64]string, len(registered))
	seenVaults := make(map[string]string, len(registered))
	for _, account := range registered {
		if previous, exists := seenLighter[account.lighterIndex]; exists {
			return AccountDiscovery{}, fmt.Errorf("Lighter account is bound to both %s and %s", previous, account.id)
		}
		vault := strings.ToLower(account.vault)
		if previous, exists := seenVaults[vault]; exists {
			return AccountDiscovery{}, fmt.Errorf("vault is bound to both %s and %s", previous, account.id)
		}
		seenLighter[account.lighterIndex] = account.id
		seenVaults[vault] = account.id
	}
	bindings, err := value.custodyBindings(ctx, registered)
	if err != nil {
		return AccountDiscovery{}, err
	}
	if err := value.receipts(ctx, registered, bindings); err != nil {
		return AccountDiscovery{}, err
	}
	result := AccountDiscovery{Accounts: make([]AccountBinding, 0, len(registered))}
	for _, account := range registered {
		binding, exists := bindings[account.id]
		if !exists {
			result.RejectedIDs = append(result.RejectedIDs, account.id)
			continue
		}
		if !strings.EqualFold(account.owner, binding.Owner) || !strings.EqualFold(account.vault, binding.Vault) ||
			!strings.EqualFold(account.signer, binding.Signer) {
			result.RejectedIDs = append(result.RejectedIDs, account.id)
			continue
		}
		result.Accounts = append(result.Accounts, AccountBinding{
			ExecutionAccountID: account.id,
			ReadinessAccountID: account.id,
			PolicyActive:       account.policyActive,
			StrategyVersion:    account.strategyVersion,
			Lighter: LighterBinding{
				AccountIndex:         account.lighterIndex,
				APIKeyIndex:          account.apiKeyIndex,
				MarketID:             value.marketID,
				MinimumCollateralRaw: value.minimums.collateral,
			},
			Robinhood: binding,
		})
	}
	return result, nil
}

func (value *PGAccountSource) registered(ctx context.Context) ([]registeredAccount, error) {
	rows, err := value.coordinator.Query(ctx, `
		SELECT registration.execution_account_id,
		       registration.lighter_account_index,
		       registration.lighter_api_key_index,
		       registration.robinhood_owner,
		       registration.robinhood_vault,
		       registration.robinhood_signer,
		       registration.strategy_version,
		       registration.strategy_manifest_sha256,
		       global.mode, strategy.mode, account_control.mode,
		       strategy.strategy_manifest_sha256, account.strategy_manifest_sha256,
		       readiness.venue_approved, readiness.oracle_healthy,
		       readiness.sequencer_healthy, readiness.reconciliation_ready,
		       readiness.exit_authority_ready, readiness.alerting_ready,
		       readiness.safe_rotation_ready, market.lighter_market_index
		FROM execution_account_registrations AS registration
		JOIN execution_accounts AS account USING (execution_account_id)
		LEFT JOIN execution_account_control AS account_control USING (execution_account_id)
		LEFT JOIN execution_account_readiness AS readiness USING (execution_account_id)
		LEFT JOIN execution_strategy_control AS strategy USING (strategy_version)
		LEFT JOIN execution_control AS global ON global.singleton
		LEFT JOIN (
			SELECT MIN(lighter_market_index) AS lighter_market_index
			FROM execution_market_configs
			WHERE symbol = 'AAPL' AND valid_from <= now() AND valid_until > now()
			HAVING COUNT(*) = 1
		) AS market ON TRUE
		WHERE account.status = 'active'
		ORDER BY registration.execution_account_id`)
	if err != nil {
		return nil, errors.New("query active execution accounts")
	}
	defer rows.Close()
	var result []registeredAccount
	for rows.Next() {
		var account registeredAccount
		var manifest string
		var policy coordinatorPolicyState
		if err := rows.Scan(
			&account.id, &account.lighterIndex, &account.apiKeyIndex, &account.owner, &account.vault, &account.signer,
			&account.strategyVersion, &manifest, &policy.globalMode, &policy.strategyMode, &policy.accountMode,
			&policy.strategyManifest, &policy.accountManifest, &policy.venueApproved,
			&policy.oracleHealthy, &policy.sequencerHealthy, &policy.reconciled,
			&policy.exitReady, &policy.alertingReady, &policy.rotationReady, &policy.lighterMarketID,
		); err != nil {
			return nil, errors.New("read active execution account")
		}
		account.policyActive = policy.Active(manifest, value.marketID)
		if !validUUID(account.id) || account.lighterIndex == 0 || account.apiKeyIndex < 4 || account.apiKeyIndex > 254 ||
			!validAddress(account.owner) || !validAddress(account.vault) || !validAddress(account.signer) {
			return nil, errors.New("authoritative execution account is invalid")
		}
		result = append(result, account)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("read active execution accounts")
	}
	return result, nil
}

func (value *PGAccountSource) custodyBindings(ctx context.Context, accounts []registeredAccount) (map[string]RobinhoodBinding, error) {
	ids := make([]string, 0, len(accounts))
	for _, account := range accounts {
		ids = append(ids, account.id)
	}
	rows, err := value.robinhood.Query(ctx, `
		SELECT execution_account_id::text, owner_address, signer_address,
		       factory_address, registry_address, vault_code_hash,
		       vault_address, risk_manager_address, spot_adapter_address,
		       deployment_tx_hash
		FROM robinhood_execution_bindings
		WHERE status = 'active' AND deployment_tx_hash IS NOT NULL
		  AND execution_account_id::text = ANY($1::text[])`, ids)
	if err != nil {
		return nil, errors.New("query active Robinhood custody bindings")
	}
	defer rows.Close()
	result := make(map[string]RobinhoodBinding, len(accounts))
	for rows.Next() {
		var id, deploymentHash string
		var binding RobinhoodBinding
		if err := rows.Scan(&id, &binding.Owner, &binding.Signer, &binding.Factory, &binding.Registry, &binding.VaultCodeHash,
			&binding.Vault, &binding.RiskManager, &binding.SpotAdapter, &deploymentHash); err != nil {
			return nil, errors.New("read active Robinhood custody binding")
		}
		if _, exists := result[id]; exists || !validHash(deploymentHash) {
			return nil, errors.New("invalid Robinhood custody binding")
		}
		binding.MinimumSettlementRaw = value.minimums.settlement
		binding.MinimumOwnerGasRaw = value.minimums.ownerGas
		binding.MinimumSignerGasRaw = value.minimums.signerGas
		binding.ReceiptHashes = []string{strings.ToLower(deploymentHash)}
		result[id] = binding
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("read active Robinhood custody bindings")
	}
	return result, nil
}

func (value *PGAccountSource) receipts(ctx context.Context, accounts []registeredAccount, bindings map[string]RobinhoodBinding) error {
	signers := make([]string, 0, len(accounts))
	byIdentity := make(map[string]string, len(accounts))
	for _, account := range accounts {
		if _, exists := bindings[account.id]; !exists {
			continue
		}
		signers = append(signers, strings.ToLower(account.signer))
		byIdentity[strings.ToLower(account.signer)+":"+strings.ToLower(account.vault)] = account.id
	}
	rows, err := value.journal.Query(ctx, `
		SELECT lower(deployment.signer_address), lower(deployment.vault_address), transaction.tx_hash
		FROM robinhood_signer_deployments AS deployment
		JOIN robinhood_signer_transactions AS transaction USING (deployment_id)
		WHERE lower(deployment.signer_address) = ANY($1::text[])
		ORDER BY transaction.created_at, transaction.request_id`, signers)
	if err != nil {
		return errors.New("query Robinhood signer receipts")
	}
	defer rows.Close()
	seen := make(map[string]map[string]struct{}, len(accounts))
	for rows.Next() {
		var signer, vault, hash string
		if err := rows.Scan(&signer, &vault, &hash); err != nil {
			return errors.New("read Robinhood signer receipt")
		}
		id, exists := byIdentity[signer+":"+vault]
		if !exists || !validHash(hash) {
			return errors.New("Robinhood signer receipt identity mismatch")
		}
		if seen[id] == nil {
			seen[id] = make(map[string]struct{})
		}
		hash = strings.ToLower(hash)
		if _, duplicate := seen[id][hash]; duplicate {
			continue
		}
		seen[id][hash] = struct{}{}
		binding := bindings[id]
		binding.ReceiptHashes = append(binding.ReceiptHashes, hash)
		bindings[id] = binding
	}
	if err := rows.Err(); err != nil {
		return errors.New("read Robinhood signer receipts")
	}
	return nil
}
