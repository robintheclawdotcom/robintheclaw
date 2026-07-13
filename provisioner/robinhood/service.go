package main

import (
	"context"
	"errors"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

var (
	errInvalidRequest = errors.New("invalid request")
	errConflict       = errors.New("binding conflict")
	errNotReady       = errors.New("binding not ready")
)

type graphVerifier interface {
	predict(context.Context, common.Address) (graph, error)
	deploymentAction(common.Address) (unsignedAction, error)
	confirm(context.Context, binding, common.Hash) (uint64, error)
	verifyActive(context.Context, binding) error
}

type executionKeyProvisioner interface {
	ensure(context.Context, string) (kmsKey, error)
}

type service struct {
	config config
	store  bindingStore
	keys   executionKeyProvisioner
	chain  graphVerifier
}

func (value *service) prepare(ctx context.Context, request prepareRequest) (publicBinding, error) {
	executionID, err := normalizeExecutionAccountID(request.ExecutionAccountID)
	if err != nil {
		return publicBinding{}, errInvalidRequest
	}
	owner, err := normalizeAddress(request.OwnerAddress)
	if err != nil {
		return publicBinding{}, errInvalidRequest
	}
	ownerAddress := common.HexToAddress(owner)
	predicted, err := value.chain.predict(ctx, ownerAddress)
	if err != nil {
		return publicBinding{}, err
	}
	key, err := value.keys.ensure(ctx, executionID)
	if err != nil {
		return publicBinding{}, err
	}
	record := binding{
		ExecutionAccountID: executionID,
		OwnerAddress:       owner,
		KMSKeyID:           key.ID,
		SignerAddress:      strings.ToLower(key.Address.Hex()),
		KeyVersion:         1,
		FactoryAddress:     strings.ToLower(value.config.FactoryAddress.Hex()),
		RegistryAddress:    strings.ToLower(value.config.RegistryAddress.Hex()),
		PolicyDigest:       strings.ToLower(value.config.PolicyDigest.Hex()),
		FactoryCodeHash:    strings.ToLower(value.config.FactoryCodeHash.Hex()),
		RegistryCodeHash:   strings.ToLower(value.config.RegistryCodeHash.Hex()),
		VaultCodeHash:      strings.ToLower(value.config.VaultCodeHash.Hex()),
		RiskCodeHash:       strings.ToLower(value.config.RiskManagerCodeHash.Hex()),
		AdapterCodeHash:    strings.ToLower(value.config.SpotAdapterCodeHash.Hex()),
		Graph:              predicted,
	}
	stored, err := value.store.Create(ctx, record)
	if err != nil {
		if errors.Is(err, errBindingConflict) {
			return publicBinding{}, errConflict
		}
		return publicBinding{}, err
	}
	if stored.Status == statusBlocked || stored.Status == statusRotationPending {
		return publicBinding{}, errNotReady
	}
	actions := []unsignedAction(nil)
	if stored.Status != statusActive {
		action, err := value.chain.deploymentAction(ownerAddress)
		if err != nil {
			return publicBinding{}, err
		}
		actions = []unsignedAction{action}
	}
	return stored.public(actions), nil
}

func (value *service) status(ctx context.Context, request statusRequest) (publicBinding, error) {
	executionID, err := normalizeExecutionAccountID(request.ExecutionAccountID)
	if err != nil {
		return publicBinding{}, errInvalidRequest
	}
	record, err := value.store.Get(ctx, executionID)
	if errors.Is(err, errNotFound) {
		return publicBinding{}, errInvalidRequest
	}
	if err != nil {
		return publicBinding{}, err
	}
	return record.public(nil), nil
}

func (value *service) confirm(ctx context.Context, request confirmRequest) (publicBinding, error) {
	executionID, err := normalizeExecutionAccountID(request.ExecutionAccountID)
	if err != nil {
		return publicBinding{}, errInvalidRequest
	}
	if len(request.DeploymentTxHash) != 66 || !strings.HasPrefix(request.DeploymentTxHash, "0x") || common.HexToHash(request.DeploymentTxHash) == (common.Hash{}) {
		return publicBinding{}, errInvalidRequest
	}
	txHash := strings.ToLower(common.HexToHash(request.DeploymentTxHash).Hex())
	record, err := value.store.Get(ctx, executionID)
	if errors.Is(err, errNotFound) {
		return publicBinding{}, errInvalidRequest
	}
	if err != nil {
		return publicBinding{}, err
	}
	if record.Status == statusActive {
		if record.DeploymentTxHash != txHash {
			return publicBinding{}, errConflict
		}
		if err := value.chain.verifyActive(ctx, record); err != nil {
			return publicBinding{}, errNotReady
		}
		return record.public(nil), nil
	}
	if record.Status == statusBlocked || record.Status == statusRotationPending {
		return publicBinding{}, errNotReady
	}
	record, err = value.store.MarkConfirming(ctx, record, txHash)
	if err != nil {
		return publicBinding{}, errConflict
	}
	block, err := value.chain.confirm(ctx, record, common.HexToHash(txHash))
	if err != nil {
		return publicBinding{}, errNotReady
	}
	record, err = value.store.Activate(ctx, record, block)
	if err != nil {
		return publicBinding{}, err
	}
	return record.public(nil), nil
}

func (value *service) resolve(ctx context.Context, request resolveRequest) (resolvedBinding, error) {
	executionID, err := normalizeExecutionAccountID(request.ExecutionAccountID)
	if err != nil {
		return resolvedBinding{}, errInvalidRequest
	}
	record, err := value.store.Get(ctx, executionID)
	if errors.Is(err, errNotFound) {
		return resolvedBinding{}, errInvalidRequest
	}
	if err != nil {
		return resolvedBinding{}, err
	}
	if record.Status != statusActive {
		return resolvedBinding{}, errNotReady
	}
	if err := value.chain.verifyActive(ctx, record); err != nil {
		return resolvedBinding{}, errNotReady
	}
	return record.resolved(), nil
}
