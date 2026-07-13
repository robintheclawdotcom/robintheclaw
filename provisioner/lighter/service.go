package main

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

type service struct {
	store             credentialStore
	envelope          *envelope
	lighter           lighterClient
	ttl               time.Duration
	now               func() time.Time
	publisherMarketID uint16
}

type prepareRequest struct {
	ExecutionAccountID string `json:"executionAccountId"`
	OwnerAddress       string `json:"ownerAddress"`
	AccountIndex       int64  `json:"accountIndex"`
	APIKeyIndex        uint8  `json:"apiKeyIndex"`
	Nonce              int64  `json:"nonce"`
}

type statusRequest struct {
	ExecutionAccountID string `json:"executionAccountId"`
}

type confirmRequest struct {
	ExecutionAccountID string `json:"executionAccountId"`
	LinkID             string `json:"linkId"`
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
	if err := validateAccount(request.AccountIndex, request.APIKeyIndex); err != nil {
		return publicLink{}, err
	}
	if request.Nonce < 0 {
		return publicLink{}, errors.New("nonce must not be negative")
	}
	request.ExecutionAccountID = strings.ToLower(request.ExecutionAccountID)
	reserved, err := value.store.Reserve(ctx, request.ExecutionAccountID, owner, request.AccountIndex, request.APIKeyIndex, request.Nonce)
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
	record.ChangeNonce = request.Nonce
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

func validL1Signature(value string) bool {
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "0x"))
	return err == nil && len(decoded) == 65
}
