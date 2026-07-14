package main

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

const testExecutionID = "11111111-1111-4111-8111-111111111111"

type fakeStore struct {
	record binding
	nonces map[string]struct{}
}

func (value *fakeStore) Create(_ context.Context, record binding) (binding, error) {
	if value.record.ExecutionAccountID != "" {
		if !sameImmutableBinding(value.record, record) {
			return binding{}, errBindingConflict
		}
		return value.record, nil
	}
	record.Status = statusAwaitingDeployment
	record.CreatedAt = time.Now().UTC()
	record.UpdatedAt = record.CreatedAt
	value.record = record
	return record, nil
}

func (value *fakeStore) Get(_ context.Context, executionID string) (binding, error) {
	if value.record.ExecutionAccountID != executionID {
		return binding{}, errNotFound
	}
	return value.record, nil
}

func (value *fakeStore) MarkConfirming(_ context.Context, record binding, txHash string) (binding, error) {
	if value.record.ExecutionAccountID != record.ExecutionAccountID ||
		(value.record.Status != statusAwaitingDeployment && (value.record.Status != statusConfirming || value.record.DeploymentTxHash != txHash)) {
		return binding{}, errBindingConflict
	}
	value.record.Status = statusConfirming
	value.record.DeploymentTxHash = txHash
	return value.record, nil
}

func (value *fakeStore) MarkDeployed(_ context.Context, record binding, block uint64) (binding, error) {
	if value.record.Status != statusConfirming || value.record.DeploymentTxHash != record.DeploymentTxHash || block == 0 {
		return binding{}, errBindingConflict
	}
	value.record.DeploymentBlock = block
	return value.record, nil
}

func (value *fakeStore) MarkAuthorized(_ context.Context, record binding, txHash string, block uint64) (binding, error) {
	if value.record.Status != statusConfirming || value.record.DeploymentTxHash != record.DeploymentTxHash || txHash == record.DeploymentTxHash {
		return binding{}, errBindingConflict
	}
	value.record.Status = statusActive
	value.record.AuthorizationTxHash = txHash
	value.record.AuthorizationBlock = block
	return value.record, nil
}

func (value *fakeStore) Block(context.Context, binding, string) error { return nil }

func (value *fakeStore) ClaimNonce(_ context.Context, caller, nonce string, _ time.Time) (bool, error) {
	if value.nonces == nil {
		value.nonces = make(map[string]struct{})
	}
	key := caller + ":" + nonce
	if _, exists := value.nonces[key]; exists {
		return false, nil
	}
	value.nonces[key] = struct{}{}
	return true, nil
}

type fakeKeys struct{ key kmsKey }

func (value fakeKeys) ensure(context.Context, string) (kmsKey, error) { return value.key, nil }

type fakeGraphVerifier struct {
	graph                  graph
	confirmed              uint64
	action                 *unsignedAction
	verifyError            error
	deploymentConfirmed    bool
	deploymentAttempts     int
	authorizationConfirmed bool
}

func (value *fakeGraphVerifier) predict(context.Context, common.Address) (graph, error) {
	return value.graph, nil
}

func (*fakeGraphVerifier) deploymentAction(common.Address) (unsignedAction, error) {
	return unsignedAction{Kind: "deploy_user_graph", ChainID: "4663", To: "0x0000000000000000000000000000000000000010", Value: "0", Data: "0x1234"}, nil
}

func (value *fakeGraphVerifier) activationAction(context.Context, binding) (*unsignedAction, error) {
	return value.action, value.verifyError
}

func (value *fakeGraphVerifier) confirmDeployment(context.Context, binding, common.Hash) (uint64, error) {
	value.deploymentConfirmed = true
	value.deploymentAttempts++
	return value.confirmed, value.verifyError
}

func (value *fakeGraphVerifier) confirmAuthorization(context.Context, binding, common.Hash) (uint64, error) {
	value.authorizationConfirmed = true
	return value.confirmed, value.verifyError
}

func (value *fakeGraphVerifier) verifyActive(context.Context, binding) error {
	return value.verifyError
}

func testService(store *fakeStore, verifier *fakeGraphVerifier) *service {
	return &service{
		config: config{
			ChainID:             bigInt(4663),
			FactoryAddress:      common.HexToAddress("0x0000000000000000000000000000000000000010"),
			RegistryAddress:     common.HexToAddress("0x0000000000000000000000000000000000000020"),
			PolicyDigest:        common.HexToHash("0x" + strings.Repeat("1", 64)),
			FactoryCodeHash:     common.HexToHash("0x" + strings.Repeat("2", 64)),
			RegistryCodeHash:    common.HexToHash("0x" + strings.Repeat("3", 64)),
			VaultCodeHash:       common.HexToHash("0x" + strings.Repeat("4", 64)),
			RiskManagerCodeHash: common.HexToHash("0x" + strings.Repeat("5", 64)),
			SpotAdapterCodeHash: common.HexToHash("0x" + strings.Repeat("6", 64)),
		},
		store: store,
		keys: fakeKeys{key: kmsKey{
			ID:      "arn:aws:kms:region:account:key/test",
			Address: common.HexToAddress("0x0000000000000000000000000000000000000030"),
		}},
		chain: verifier,
	}
}

func TestPrepareReturnsOnlyUnsignedDeploymentAction(t *testing.T) {
	store := &fakeStore{}
	verifier := &fakeGraphVerifier{graph: graph{
		RiskManager: "0x0000000000000000000000000000000000000040",
		SpotAdapter: "0x0000000000000000000000000000000000000050",
		Vault:       "0x0000000000000000000000000000000000000060",
	}}
	result, err := testService(store, verifier).prepare(context.Background(), prepareRequest{
		ExecutionAccountID: testExecutionID,
		OwnerAddress:       "0x0000000000000000000000000000000000000070",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Actions) != 1 || result.Actions[0].Kind != "deploy_user_graph" || result.Actions[0].Value != "0" {
		t.Fatalf("unexpected deployment actions: %#v", result.Actions)
	}
	if strings.Contains(result.Actions[0].Data, store.record.KMSKeyID) || result.SignerAddress == "" {
		t.Fatal("prepare exposed a private key reference or omitted the public signer")
	}
}

func TestPrepareRejectsCrossAccountOwnerSubstitution(t *testing.T) {
	store := &fakeStore{}
	verifier := &fakeGraphVerifier{graph: graph{
		RiskManager: "0x0000000000000000000000000000000000000040",
		SpotAdapter: "0x0000000000000000000000000000000000000050",
		Vault:       "0x0000000000000000000000000000000000000060",
	}}
	service := testService(store, verifier)
	_, err := service.prepare(context.Background(), prepareRequest{ExecutionAccountID: testExecutionID, OwnerAddress: "0x0000000000000000000000000000000000000070"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.prepare(context.Background(), prepareRequest{ExecutionAccountID: testExecutionID, OwnerAddress: "0x0000000000000000000000000000000000000080"})
	if !errors.Is(err, errConflict) {
		t.Fatalf("owner substitution was not rejected: %v", err)
	}
}

func TestResolveRequiresActiveVerifiedBinding(t *testing.T) {
	store := &fakeStore{}
	verifier := &fakeGraphVerifier{graph: graph{
		RiskManager: "0x0000000000000000000000000000000000000040",
		SpotAdapter: "0x0000000000000000000000000000000000000050",
		Vault:       "0x0000000000000000000000000000000000000060",
	}}
	service := testService(store, verifier)
	_, err := service.prepare(context.Background(), prepareRequest{ExecutionAccountID: testExecutionID, OwnerAddress: "0x0000000000000000000000000000000000000070"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.resolve(context.Background(), resolveRequest{ExecutionAccountID: testExecutionID}); !errors.Is(err, errNotReady) {
		t.Fatalf("unconfirmed binding resolved: %v", err)
	}
	store.record.Status = statusActive
	resolved, err := service.resolve(context.Background(), resolveRequest{ExecutionAccountID: testExecutionID})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.KMSKeyID == "" || resolved.BindingSHA256 == "" || resolved.ExecutionAccountID != testExecutionID {
		t.Fatalf("invalid private binding: %#v", resolved)
	}
	verifier.verifyError = errors.New("agent mismatch")
	if _, err := service.resolve(context.Background(), resolveRequest{ExecutionAccountID: testExecutionID}); !errors.Is(err, errNotReady) {
		t.Fatalf("mismatched graph resolved: %v", err)
	}
}

func TestConfirmRequiresOwnerAgentAuthorizationAfterFinalDeployment(t *testing.T) {
	store := &fakeStore{}
	verifier := &fakeGraphVerifier{
		graph: graph{
			RiskManager: "0x0000000000000000000000000000000000000040",
			SpotAdapter: "0x0000000000000000000000000000000000000050",
			Vault:       "0x0000000000000000000000000000000000000060",
		},
		confirmed: 123,
		action: &unsignedAction{
			Kind: "authorize_execution_agent", ChainID: "4663",
			To: "0x0000000000000000000000000000000000000060", Value: "0", Data: "0x6d88b24d",
		},
	}
	service := testService(store, verifier)
	_, err := service.prepare(context.Background(), prepareRequest{ExecutionAccountID: testExecutionID, OwnerAddress: "0x0000000000000000000000000000000000000070"})
	if err != nil {
		t.Fatal(err)
	}
	txHash := "0x" + strings.Repeat("a", 64)
	result, err := service.confirm(context.Background(), confirmRequest{ExecutionAccountID: testExecutionID, TransactionHash: txHash})
	if err != nil {
		t.Fatal(err)
	}
	if !verifier.deploymentConfirmed || result.Status != statusConfirming || result.DeploymentBlock != 123 || result.DeploymentTxHash != txHash || len(result.Actions) != 1 {
		t.Fatalf("unexpected confirmation: %#v", result)
	}

	authorizationHash := "0x" + strings.Repeat("b", 64)
	verifier.action = nil
	result, err = service.confirm(context.Background(), confirmRequest{ExecutionAccountID: testExecutionID, TransactionHash: authorizationHash})
	if err != nil {
		t.Fatal(err)
	}
	if !verifier.authorizationConfirmed || result.Status != statusActive || result.AuthorizationTxHash != authorizationHash || len(result.Actions) != 0 {
		t.Fatalf("authorized graph did not activate: %#v", result)
	}
}

func TestConfirmDoesNotReuseDeploymentProofAsAuthorization(t *testing.T) {
	store := &fakeStore{record: binding{
		ExecutionAccountID: testExecutionID,
		DeploymentTxHash:   "0x" + strings.Repeat("a", 64),
		DeploymentBlock:    123,
		Status:             statusConfirming,
	}}
	verifier := &fakeGraphVerifier{action: &unsignedAction{Kind: "authorize_execution_agent"}}
	result, err := testService(store, verifier).confirm(context.Background(), confirmRequest{
		ExecutionAccountID: testExecutionID,
		TransactionHash:    store.record.DeploymentTxHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != statusConfirming || verifier.authorizationConfirmed {
		t.Fatalf("deployment proof was reused as authorization: %#v", result)
	}
}

func TestConfirmRetriesDeploymentAfterFinalityDelay(t *testing.T) {
	store := &fakeStore{}
	verifier := &fakeGraphVerifier{
		graph: graph{
			RiskManager: "0x0000000000000000000000000000000000000040",
			SpotAdapter: "0x0000000000000000000000000000000000000050",
			Vault:       "0x0000000000000000000000000000000000000060",
		},
		confirmed:   123,
		verifyError: errors.New("not final"),
		action:      &unsignedAction{Kind: "authorize_execution_agent"},
	}
	service := testService(store, verifier)
	_, err := service.prepare(context.Background(), prepareRequest{
		ExecutionAccountID: testExecutionID,
		OwnerAddress:       "0x0000000000000000000000000000000000000070",
	})
	if err != nil {
		t.Fatal(err)
	}
	txHash := "0x" + strings.Repeat("a", 64)
	_, err = service.confirm(context.Background(), confirmRequest{
		ExecutionAccountID: testExecutionID,
		TransactionHash:    txHash,
	})
	if !errors.Is(err, errNotReady) || store.record.Status != statusConfirming || store.record.DeploymentBlock != 0 {
		t.Fatalf("expected resumable finality delay, got record=%#v error=%v", store.record, err)
	}

	verifier.verifyError = nil
	result, err := service.confirm(context.Background(), confirmRequest{
		ExecutionAccountID: testExecutionID,
		TransactionHash:    txHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if verifier.deploymentAttempts != 2 || result.DeploymentBlock != 123 || len(result.Actions) != 1 {
		t.Fatalf("deployment confirmation did not resume: %#v", result)
	}
}

func bigInt(value int64) *big.Int { return big.NewInt(value) }
