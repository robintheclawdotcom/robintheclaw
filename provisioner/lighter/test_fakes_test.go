package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
)

type memoryStore struct {
	mu       sync.Mutex
	bindings map[string]binding
	records  map[string]credential
	versions map[string][]string
	nonces   map[string]time.Time
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		bindings: make(map[string]binding),
		records:  make(map[string]credential),
		versions: make(map[string][]string),
		nonces:   make(map[string]time.Time),
	}
}

func (value *memoryStore) Reserve(_ context.Context, executionID, owner string, accountIndex int64, apiKeyIndex uint8) (reservation, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	bound, exists := value.bindings[executionID]
	if exists && (bound.OwnerAddress != owner || bound.AccountIndex != accountIndex || bound.APIKeyIndex != apiKeyIndex) {
		return reservation{}, errBindingMismatch
	}
	for _, id := range value.versions[executionID] {
		status := value.records[id].Status
		if status == statusGenerating || status == statusPending || status == statusVerifying {
			return reservation{}, errRotationOpen
		}
	}
	id, err := newUUID()
	if err != nil {
		return reservation{}, err
	}
	now := time.Now().UTC()
	record := credential{
		ID:                 id,
		ExecutionAccountID: executionID,
		OwnerAddress:       owner,
		AccountIndex:       accountIndex,
		APIKeyIndex:        apiKeyIndex,
		Version:            int64(len(value.versions[executionID]) + 1),
		Status:             statusGenerating,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	rotation := exists && bound.ActiveCredentialID != ""
	bound = binding{
		ExecutionAccountID: executionID,
		OwnerAddress:       owner,
		AccountIndex:       accountIndex,
		APIKeyIndex:        apiKeyIndex,
		Status:             "pending",
		ActiveCredentialID: bound.ActiveCredentialID,
	}
	if rotation {
		bound.Status = "rotation_pending"
	}
	value.bindings[executionID] = bound
	value.records[id] = record
	value.versions[executionID] = append(value.versions[executionID], id)
	return reservation{Credential: record, Rotation: rotation}, nil
}

func (value *memoryStore) Complete(_ context.Context, record credential) (credential, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	current, exists := value.records[record.ID]
	if !exists || current.Status != statusGenerating {
		return credential{}, errNotFound
	}
	record.Status = statusPending
	record.CreatedAt = current.CreatedAt
	record.UpdatedAt = time.Now().UTC()
	value.records[record.ID] = cloneCredential(record)
	return cloneCredential(record), nil
}

func (value *memoryStore) Fail(_ context.Context, id, _ string) error {
	value.mu.Lock()
	defer value.mu.Unlock()
	record, exists := value.records[id]
	if !exists {
		return errNotFound
	}
	record.Status = statusBlocked
	value.records[id] = record
	bound := value.bindings[record.ExecutionAccountID]
	bound.Status = "blocked"
	value.bindings[record.ExecutionAccountID] = bound
	return nil
}

func (value *memoryStore) Latest(_ context.Context, executionID string) (credential, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	ids := value.versions[executionID]
	if len(ids) == 0 {
		return credential{}, errNotFound
	}
	return cloneCredential(value.records[ids[len(ids)-1]]), nil
}

func (value *memoryStore) Get(_ context.Context, executionID, id string) (credential, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	record, exists := value.records[id]
	if !exists || record.ExecutionAccountID != executionID {
		return credential{}, errNotFound
	}
	return cloneCredential(record), nil
}

func (value *memoryStore) MarkVerifying(_ context.Context, record credential, signature string, txInfo []byte) (credential, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	current := value.records[record.ID]
	if current.Status != statusPending {
		return credential{}, errors.New("invalid transition")
	}
	current.Status = statusVerifying
	current.L1Signature = signature
	current.TxInfo = append([]byte(nil), txInfo...)
	current.UpdatedAt = time.Now().UTC()
	value.records[record.ID] = current
	bound := value.bindings[record.ExecutionAccountID]
	bound.Status = "verifying"
	value.bindings[record.ExecutionAccountID] = bound
	return cloneCredential(current), nil
}

func (value *memoryStore) Activate(_ context.Context, record credential) (credential, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	current := value.records[record.ID]
	if current.Status != statusVerifying {
		return credential{}, errors.New("invalid transition")
	}
	for _, id := range value.versions[record.ExecutionAccountID] {
		other := value.records[id]
		if other.Status == statusLinked {
			other.Status = statusSuperseded
			value.records[id] = other
		}
	}
	current.Status = statusLinked
	current.UpdatedAt = time.Now().UTC()
	value.records[record.ID] = current
	bound := value.bindings[record.ExecutionAccountID]
	bound.Status = "linked"
	bound.ActiveCredentialID = record.ID
	value.bindings[record.ExecutionAccountID] = bound
	return cloneCredential(current), nil
}

func (value *memoryStore) Block(ctx context.Context, record credential, reason string) error {
	return value.Fail(ctx, record.ID, reason)
}

func (value *memoryStore) Active(_ context.Context, executionID string) (credential, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	bound, exists := value.bindings[executionID]
	if !exists || bound.Status != "linked" || bound.ActiveCredentialID == "" {
		return credential{}, errNotFound
	}
	record := value.records[bound.ActiveCredentialID]
	if record.Status != statusLinked {
		return credential{}, errNotFound
	}
	return cloneCredential(record), nil
}

func (value *memoryStore) ClaimAuthNonce(_ context.Context, caller, nonce string, expiresAt time.Time) (bool, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	key := caller + ":" + nonce
	if _, exists := value.nonces[key]; exists {
		return false, nil
	}
	value.nonces[key] = expiresAt
	return true, nil
}

func (*memoryStore) Audit(context.Context, credential, string, map[string]any) error {
	return nil
}

func cloneCredential(value credential) credential {
	value.EncryptedDataKey = append([]byte(nil), value.EncryptedDataKey...)
	value.CipherNonce = append([]byte(nil), value.CipherNonce...)
	value.Ciphertext = append([]byte(nil), value.Ciphertext...)
	value.AADDigest = append([]byte(nil), value.AADDigest...)
	value.TxInfo = append([]byte(nil), value.TxInfo...)
	return value
}

type fakeKMS struct {
	context      map[string]string
	decryptCalls int
}

func (value *fakeKMS) GenerateDataKey(_ context.Context, input *awskms.GenerateDataKeyInput, _ ...func(*awskms.Options)) (*awskms.GenerateDataKeyOutput, error) {
	value.context = copyStringMap(input.EncryptionContext)
	return &awskms.GenerateDataKeyOutput{
		CiphertextBlob: []byte("wrapped-data-key"),
		KeyId:          input.KeyId,
		Plaintext:      []byte("0123456789abcdef0123456789abcdef"),
	}, nil
}

func (value *fakeKMS) Decrypt(_ context.Context, input *awskms.DecryptInput, _ ...func(*awskms.Options)) (*awskms.DecryptOutput, error) {
	value.decryptCalls++
	if !reflect.DeepEqual(value.context, input.EncryptionContext) || string(input.CiphertextBlob) != "wrapped-data-key" {
		return nil, errors.New("context mismatch")
	}
	return &awskms.DecryptOutput{
		KeyId:     input.KeyId,
		Plaintext: []byte("0123456789abcdef0123456789abcdef"),
	}, nil
}

func copyStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

type fakeLighter struct {
	mu             sync.Mutex
	generated      int
	recoveredOwner string
	registered     string
	broadcastErr   error
	broadcasts     int
}

func (value *fakeLighter) GenerateKey() (string, string, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	value.generated++
	return fmt.Sprintf("generated-credential-%d", value.generated), fmt.Sprintf("0x%080x", value.generated), nil
}

func (*fakeLighter) BuildAssociation(_ string, public string, accountIndex int64, apiKeyIndex uint8, nonce, expiresAtMS int64) (association, error) {
	return fakeAssociation(public, accountIndex, apiKeyIndex, nonce, expiresAtMS, ""), nil
}

func (value *fakeLighter) FinalizeAssociation(_ string, public string, accountIndex int64, apiKeyIndex uint8, nonce, expiresAtMS int64, signature string) (association, string, error) {
	return fakeAssociation(public, accountIndex, apiKeyIndex, nonce, expiresAtMS, signature), value.recoveredOwner, nil
}

func fakeAssociation(public string, accountIndex int64, apiKeyIndex uint8, nonce, expiresAtMS int64, signature string) association {
	return association{
		TxType:        8,
		TxHash:        fmt.Sprintf("0x%064x", nonce+accountIndex+int64(apiKeyIndex)),
		TxInfo:        []byte(fmt.Sprintf(`{"public":%q,"expiry":%d,"signature":%q}`, public, expiresAtMS, signature)),
		MessageToSign: fmt.Sprintf("associate %s account %d key %d nonce %d", public, accountIndex, apiKeyIndex, nonce),
	}
}

func (value *fakeLighter) Broadcast(_ context.Context, _ association) error {
	value.mu.Lock()
	defer value.mu.Unlock()
	value.broadcasts++
	return value.broadcastErr
}

func (value *fakeLighter) RegisteredPublicKey(_ int64, _ uint8) (string, error) {
	if value.registered == "" {
		return "", errors.New("not registered")
	}
	return value.registered, nil
}

func (*fakeLighter) AuthToken(_ string, accountIndex int64, apiKeyIndex uint8, expiresAt time.Time) (string, error) {
	return fmt.Sprintf("auth-%d-%d-%d", accountIndex, apiKeyIndex, expiresAt.Unix()), nil
}

func validTestSignature() string {
	return "0x" + strings.Repeat("11", 65)
}
