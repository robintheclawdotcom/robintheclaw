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

func (value *fakeStore) Activate(_ context.Context, record binding, block uint64) (binding, error) {
	if value.record.Status != statusConfirming || value.record.DeploymentTxHash != record.DeploymentTxHash {
		return binding{}, errBindingConflict
	}
	value.record.Status = statusActive
	value.record.DeploymentBlock = block
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
	graph         graph
	confirmed     uint64
	verifyError   error
	confirmCalled bool
}

func (value *fakeGraphVerifier) predict(context.Context, common.Address) (graph, error) {
	return value.graph, nil
}

func (*fakeGraphVerifier) deploymentAction(common.Address) (unsignedAction, error) {
	return unsignedAction{Kind: "deploy_user_graph", ChainID: "4663", To: "0x0000000000000000000000000000000000000010", Value: "0", Data: "0x1234"}, nil
}

func (value *fakeGraphVerifier) confirm(context.Context, binding, common.Hash) (uint64, error) {
	value.confirmCalled = true
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

func TestConfirmActivatesOnlyAfterFinalGraphVerification(t *testing.T) {
	store := &fakeStore{}
	verifier := &fakeGraphVerifier{
		graph: graph{
			RiskManager: "0x0000000000000000000000000000000000000040",
			SpotAdapter: "0x0000000000000000000000000000000000000050",
			Vault:       "0x0000000000000000000000000000000000000060",
		},
		confirmed: 123,
	}
	service := testService(store, verifier)
	_, err := service.prepare(context.Background(), prepareRequest{ExecutionAccountID: testExecutionID, OwnerAddress: "0x0000000000000000000000000000000000000070"})
	if err != nil {
		t.Fatal(err)
	}
	txHash := "0x" + strings.Repeat("a", 64)
	result, err := service.confirm(context.Background(), confirmRequest{ExecutionAccountID: testExecutionID, DeploymentTxHash: txHash})
	if err != nil {
		t.Fatal(err)
	}
	if !verifier.confirmCalled || result.Status != statusActive || result.DeploymentBlock != 123 || result.DeploymentTxHash != txHash {
		t.Fatalf("unexpected confirmation: %#v", result)
	}
}

func bigInt(value int64) *big.Int { return big.NewInt(value) }
