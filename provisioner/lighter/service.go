package main

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

type service struct {
	store               credentialStore
	envelope            *envelope
	lighter             lighterClient
	ttl                 time.Duration
	now                 func() time.Time
	publisherMarketID   uint16
	marketBaseDecimals  uint8
	marketPriceDecimals uint8
}

type prepareRequest struct {
	ExecutionAccountID string `json:"executionAccountId"`
	OwnerAddress       string `json:"ownerAddress"`
	APIKeyIndex        uint8  `json:"apiKeyIndex"`
}

type statusRequest struct {
	ExecutionAccountID string `json:"executionAccountId"`
}

type confirmRequest struct {
	ExecutionAccountID string `json:"executionAccountId"`
	LinkID             string `json:"linkId"`
	L1Signature        string `json:"l1Signature"`
}

type revocationRequest struct {
	ExecutionAccountID string `json:"executionAccountId"`
}

type confirmRevocationRequest struct {
	ExecutionAccountID string `json:"executionAccountId"`
	RevocationID       string `json:"revocationId"`
	L1Signature        string `json:"l1Signature"`
}

func (value *service) prepare(ctx context.Context, request prepareRequest) (publicLink, error) {
	if err := validateExecutionAccountID(request.ExecutionAccountID); err != nil {
		return publicLink{}, err
	}
	owner, err := normalizeAddress(request.OwnerAddress)
	if err != nil {
		return publicLink{}, err
	}
	if request.APIKeyIndex < 4 || request.APIKeyIndex > 254 {
		return publicLink{}, errors.New("apiKeyIndex must be between 4 and 254")
	}
	request.ExecutionAccountID = strings.ToLower(request.ExecutionAccountID)
	existing, err := value.store.Latest(ctx, request.ExecutionAccountID)
	if err == nil && (existing.Status == statusGenerating || existing.Status == statusPending || existing.Status == statusVerifying) {
		if existing.Purpose == purposeRevocation {
			return publicLink{}, errors.New("credential revocation is already in progress")
		}
		if existing.OwnerAddress != owner || existing.APIKeyIndex != request.APIKeyIndex {
			return publicLink{}, errBindingMismatch
		}
		if existing.Status == statusGenerating {
			return publicLink{}, errRotationOpen
		}
		return existing.public(), nil
	}
	if err != nil && !errors.Is(err, errNotFound) {
		return publicLink{}, err
	}
	accountIndex, err := value.lighter.DiscoverEmptySubaccount(ctx, owner)
	if err != nil {
		return publicLink{}, err
	}
	nonce, err := value.lighter.NextNonce(ctx, accountIndex, request.APIKeyIndex)
	if err != nil {
		return publicLink{}, err
	}
	reserved, err := value.store.Reserve(ctx, request.ExecutionAccountID, owner, accountIndex, request.APIKeyIndex, nonce)
	if err != nil {
		return publicLink{}, err
	}
	if reserved.Existing {
		return reserved.Credential.public(), nil
	}
	record := reserved.Credential
	failed := true
	defer func() {
		if failed {
			_ = value.store.Fail(context.WithoutCancel(ctx), record.ID, "provisioning_failed")
		}
	}()

	secret, publicKey, err := value.lighter.GenerateKey()
	if err != nil {
		return publicLink{}, errors.New("generate Lighter credential")
	}
	secretBytes := []byte(secret)
	defer zero(secretBytes)
	record.PublicKey = publicKey
	record.ChangeNonce = nonce
	record.ExpiresAtMS = value.now().Add(value.ttl).UnixMilli()
	association, err := value.lighter.BuildAssociation(
		secret,
		publicKey,
		record.AccountIndex,
		record.APIKeyIndex,
		record.ChangeNonce,
		record.ExpiresAtMS,
	)
	if err != nil {
		return publicLink{}, err
	}
	sealed, err := value.envelope.seal(ctx, record, secretBytes)
	if err != nil {
		return publicLink{}, err
	}
	record.EncryptedDataKey = sealed.EncryptedDataKey
	record.CipherNonce = sealed.Nonce
	record.Ciphertext = sealed.Ciphertext
	record.AADDigest = sealed.AADDigest
	record.KMSKeyID = sealed.KMSKeyID
	record.TxType = association.TxType
	record.TxHash = association.TxHash
	record.TxInfo = association.TxInfo
	record.MessageToSign = association.MessageToSign
	record, err = value.store.Complete(ctx, record)
	if err != nil {
		return publicLink{}, err
	}
	failed = false
	return record.public(), nil
}

func (value *service) status(ctx context.Context, request statusRequest) (publicLink, error) {
	if err := validateExecutionAccountID(request.ExecutionAccountID); err != nil {
		return publicLink{}, err
	}
	record, err := value.store.Latest(ctx, strings.ToLower(request.ExecutionAccountID))
	if err != nil {
		return publicLink{}, err
	}
	return record.public(), nil
}

func (value *service) confirm(ctx context.Context, request confirmRequest) (publicLink, bool, error) {
	if err := validateExecutionAccountID(request.ExecutionAccountID); err != nil {
		return publicLink{}, false, err
	}
	if err := validateExecutionAccountID(request.LinkID); err != nil {
		return publicLink{}, false, errors.New("linkId must be a UUID")
	}
	if !validL1Signature(request.L1Signature) {
		return publicLink{}, false, errors.New("l1Signature must be a 65-byte hex signature")
	}
	record, err := value.store.Get(ctx, strings.ToLower(request.ExecutionAccountID), strings.ToLower(request.LinkID))
	if err != nil {
		return publicLink{}, false, err
	}
	if record.Purpose == purposeRevocation {
		return publicLink{}, false, errors.New("revocation credentials require the revocation confirmation route")
	}
	if record.Status == statusLinked {
		return record.public(), true, nil
	}
	if record.Status == statusBlocked || record.Status == statusSuperseded || record.Status == statusGenerating {
		return publicLink{}, false, errors.New("credential is not confirmable")
	}

	if record.Status == statusPending {
		secretBytes, err := value.envelope.open(ctx, record)
		if err != nil {
			_ = value.store.Block(context.WithoutCancel(ctx), record, "decrypt_failed")
			return publicLink{}, false, err
		}
		finalized, recoveredOwner, err := value.lighter.FinalizeAssociation(
			transientString(secretBytes),
			record.PublicKey,
			record.AccountIndex,
			record.APIKeyIndex,
			record.ChangeNonce,
			record.ExpiresAtMS,
			request.L1Signature,
		)
		zero(secretBytes)
		if err != nil || !strings.EqualFold(recoveredOwner, record.OwnerAddress) {
			_ = value.store.Block(context.WithoutCancel(ctx), record, "owner_signature_mismatch")
			return publicLink{}, false, errors.New("association signature does not match bound owner")
		}
		if finalized.TxType != record.TxType ||
			!strings.EqualFold(finalized.TxHash, record.TxHash) ||
			finalized.MessageToSign != record.MessageToSign {
			_ = value.store.Block(context.WithoutCancel(ctx), record, "association_reconstruction_mismatch")
			return publicLink{}, false, errors.New("association reconstruction mismatch")
		}
		record, err = value.store.MarkVerifying(ctx, record, request.L1Signature, finalized.TxInfo)
		if err != nil {
			return publicLink{}, false, err
		}
		if err := value.lighter.Broadcast(ctx, finalized); err != nil {
			if errors.Is(err, errAmbiguousSubmission) {
				if auditErr := value.store.Audit(ctx, record, "association_submission_ambiguous", map[string]any{
					"transactionHash": record.TxHash,
				}); auditErr != nil {
					return publicLink{}, false, auditErr
				}
				return record.public(), false, nil
			}
			_ = value.store.Block(context.WithoutCancel(ctx), record, "association_submission_rejected")
			return publicLink{}, false, err
		}
		if err := value.store.Audit(ctx, record, "association_submission_accepted", map[string]any{
			"transactionHash": record.TxHash,
		}); err != nil {
			return publicLink{}, false, err
		}
	}

	registered, err := value.lighter.RegisteredPublicKey(record.AccountIndex, record.APIKeyIndex)
	if err != nil {
		return record.public(), false, nil
	}
	if normalizePublicKey(registered) != normalizePublicKey(record.PublicKey) {
		_ = value.store.Block(context.WithoutCancel(ctx), record, "registered_public_key_mismatch")
		return publicLink{}, false, errors.New("registered Lighter key does not match provisioned key")
	}
	record, err = value.store.Activate(ctx, record)
	if err != nil {
		return publicLink{}, false, err
	}
	return record.public(), true, nil
}

func (value *service) prepareRevocation(ctx context.Context, request revocationRequest) (publicRevocation, error) {
	if err := validateExecutionAccountID(request.ExecutionAccountID); err != nil {
		return publicRevocation{}, err
	}
	executionID := strings.ToLower(request.ExecutionAccountID)
	latest, err := value.store.Latest(ctx, executionID)
	if err == nil && latest.Purpose == purposeRevocation && latest.Status == statusRevoked {
		return latest.revocationPublic(), nil
	}
	if err != nil && !errors.Is(err, errNotFound) {
		return publicRevocation{}, err
	}
	reserved, err := value.store.ReserveRevocation(ctx, executionID)
	if err != nil {
		return publicRevocation{}, err
	}
	if reserved.Existing {
		if reserved.Tombstone.Status != statusPending ||
			reserved.Tombstone.ExpiresAtMS > value.now().UnixMilli() {
			return reserved.Tombstone.revocationPublic(), nil
		}
		reserved, err = value.store.ReplaceExpiredRevocation(ctx, reserved.Tombstone, value.now())
		if err != nil {
			return publicRevocation{}, err
		}
	}
	record := reserved.Tombstone
	failed := true
	defer func() {
		if failed {
			_ = value.store.Fail(context.WithoutCancel(ctx), record.ID, "revocation_provisioning_failed")
		}
	}()

	registered, err := value.lighter.RegisteredPublicKey(record.AccountIndex, record.APIKeyIndex)
	if err != nil {
		return publicRevocation{}, errors.New("verify active Lighter credential")
	}
	if normalizePublicKey(registered) != normalizePublicKey(reserved.Active.PublicKey) {
		return publicRevocation{}, errors.New("registered Lighter key does not match active credential")
	}
	nonce, err := value.lighter.NextNonce(ctx, record.AccountIndex, record.APIKeyIndex)
	if err != nil {
		return publicRevocation{}, err
	}
	if err := value.store.VerifyRevocationNonce(ctx, reserved.Active, nonce); err != nil {
		return publicRevocation{}, err
	}
	secret, publicKey, err := value.lighter.GenerateKey()
	if err != nil {
		return publicRevocation{}, errors.New("generate Lighter tombstone credential")
	}
	secretBytes := []byte(secret)
	defer zero(secretBytes)
	if normalizePublicKey(publicKey) == normalizePublicKey(reserved.Active.PublicKey) {
		return publicRevocation{}, errors.New("tombstone key matches active credential")
	}
	record.PublicKey = publicKey
	record.ChangeNonce = nonce
	record.ExpiresAtMS = value.now().Add(value.ttl).UnixMilli()
	association, err := value.lighter.BuildAssociation(
		secret,
		publicKey,
		record.AccountIndex,
		record.APIKeyIndex,
		record.ChangeNonce,
		record.ExpiresAtMS,
	)
	if err != nil {
		return publicRevocation{}, err
	}
	sealed, err := value.envelope.seal(ctx, record, secretBytes)
	if err != nil {
		return publicRevocation{}, err
	}
	record.EncryptedDataKey = sealed.EncryptedDataKey
	record.CipherNonce = sealed.Nonce
	record.Ciphertext = sealed.Ciphertext
	record.AADDigest = sealed.AADDigest
	record.KMSKeyID = sealed.KMSKeyID
	record.TxType = association.TxType
	record.TxHash = association.TxHash
	record.TxInfo = association.TxInfo
	record.MessageToSign = association.MessageToSign
	record, err = value.store.CompleteRevocation(ctx, record)
	if err != nil {
		return publicRevocation{}, err
	}
	failed = false
	return record.revocationPublic(), nil
}

func (value *service) revocationStatus(ctx context.Context, request revocationRequest) (publicRevocation, bool, error) {
	if err := validateExecutionAccountID(request.ExecutionAccountID); err != nil {
		return publicRevocation{}, false, err
	}
	record, err := value.store.Latest(ctx, strings.ToLower(request.ExecutionAccountID))
	if err != nil {
		return publicRevocation{}, false, err
	}
	if record.Purpose != purposeRevocation {
		return publicRevocation{}, false, errors.New("execution account has no credential revocation")
	}
	return value.reconcileRevocation(ctx, record)
}

func (value *service) confirmRevocation(ctx context.Context, request confirmRevocationRequest) (publicRevocation, bool, error) {
	if err := validateExecutionAccountID(request.ExecutionAccountID); err != nil {
		return publicRevocation{}, false, err
	}
	if err := validateExecutionAccountID(request.RevocationID); err != nil {
		return publicRevocation{}, false, errors.New("revocationId must be a UUID")
	}
	if !validL1Signature(request.L1Signature) {
		return publicRevocation{}, false, errors.New("l1Signature must be a 65-byte hex signature")
	}
	record, err := value.store.Get(
		ctx,
		strings.ToLower(request.ExecutionAccountID),
		strings.ToLower(request.RevocationID),
	)
	if err != nil {
		return publicRevocation{}, false, err
	}
	if record.Purpose != purposeRevocation {
		return publicRevocation{}, false, errors.New("credential is not a revocation tombstone")
	}
	if record.Status == statusRevoked {
		return record.revocationPublic(), true, nil
	}
	if record.Status == statusBlocked || record.Status == statusSuperseded ||
		record.Status == statusGenerating || record.Status == statusLinked {
		return publicRevocation{}, false, errors.New("credential revocation is not confirmable")
	}

	if record.Status == statusPending {
		if record.ExpiresAtMS <= value.now().UnixMilli() {
			return record.revocationPublic(), false, errors.New(
				"revocation signature payload expired; prepare a replacement",
			)
		}
		secretBytes, err := value.envelope.open(ctx, record)
		if err != nil {
			_ = value.store.Block(context.WithoutCancel(ctx), record, "revocation_decrypt_failed")
			return publicRevocation{}, false, err
		}
		finalized, recoveredOwner, err := value.lighter.FinalizeAssociation(
			transientString(secretBytes),
			record.PublicKey,
			record.AccountIndex,
			record.APIKeyIndex,
			record.ChangeNonce,
			record.ExpiresAtMS,
			request.L1Signature,
		)
		zero(secretBytes)
		if err != nil || !strings.EqualFold(recoveredOwner, record.OwnerAddress) {
			_ = value.store.Block(context.WithoutCancel(ctx), record, "revocation_owner_signature_mismatch")
			return publicRevocation{}, false, errors.New("revocation signature does not match bound owner")
		}
		if finalized.TxType != record.TxType ||
			!strings.EqualFold(finalized.TxHash, record.TxHash) ||
			finalized.MessageToSign != record.MessageToSign {
			_ = value.store.Block(context.WithoutCancel(ctx), record, "revocation_reconstruction_mismatch")
			return publicRevocation{}, false, errors.New("revocation reconstruction mismatch")
		}
		record, err = value.store.MarkRevocationVerifying(ctx, record, request.L1Signature, finalized.TxInfo)
		if err != nil {
			return publicRevocation{}, false, err
		}
		if err := value.lighter.Broadcast(ctx, finalized); err != nil {
			if errors.Is(err, errAmbiguousSubmission) {
				if auditErr := value.store.Audit(ctx, record, "revocation_submission_ambiguous", map[string]any{
					"transactionHash": record.TxHash,
				}); auditErr != nil {
					return publicRevocation{}, false, auditErr
				}
				return record.revocationPublic(), false, nil
			}
			_ = value.store.Block(context.WithoutCancel(ctx), record, "revocation_submission_rejected")
			return publicRevocation{}, false, err
		}
		if err := value.store.Audit(ctx, record, "revocation_submission_accepted", map[string]any{
			"transactionHash": record.TxHash,
		}); err != nil {
			return publicRevocation{}, false, err
		}
	}
	return value.reconcileRevocation(ctx, record)
}

func (value *service) reconcileRevocation(ctx context.Context, record credential) (publicRevocation, bool, error) {
	if record.Status == statusRevoked {
		return record.revocationPublic(), true, nil
	}
	if record.Status != statusVerifying || record.Purpose != purposeRevocation {
		return record.revocationPublic(), false, nil
	}
	active, err := value.store.Get(ctx, record.ExecutionAccountID, record.ReplacesCredentialID)
	if err != nil {
		return publicRevocation{}, false, err
	}
	registered, err := value.lighter.RegisteredPublicKey(record.AccountIndex, record.APIKeyIndex)
	if err != nil {
		return record.revocationPublic(), false, nil
	}
	switch normalizePublicKey(registered) {
	case normalizePublicKey(active.PublicKey):
		return record.revocationPublic(), false, nil
	case normalizePublicKey(record.PublicKey):
		record, err = value.store.FinalizeRevocation(ctx, record, registered)
		if err != nil {
			return publicRevocation{}, false, err
		}
		return record.revocationPublic(), true, nil
	default:
		_ = value.store.Block(context.WithoutCancel(ctx), record, "registered_revocation_key_mismatch")
		return publicRevocation{}, false, errors.New("registered Lighter key matches neither active nor tombstone credential")
	}
}

func validL1Signature(value string) bool {
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "0x"))
	return err == nil && len(decoded) == 65
}
