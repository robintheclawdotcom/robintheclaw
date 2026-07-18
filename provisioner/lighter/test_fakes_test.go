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
	"github.com/elliottech/lighter-go/types/txtypes"
)

type memoryStore struct {
	mu         sync.Mutex
	bindings   map[string]binding
	records    map[string]credential
	versions   map[string][]string
	nonces     map[string]time.Time
	nextNonces map[string]uint64
	signing    map[string]memorySigningRequest
}

type memorySigningRequest struct {
	intentID string
	digest   string
	result   *signedTransaction
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		bindings:   make(map[string]binding),
		records:    make(map[string]credential),
		versions:   make(map[string][]string),
		nonces:     make(map[string]time.Time),
		nextNonces: make(map[string]uint64),
		signing:    make(map[string]memorySigningRequest),
	}
}

func (value *memoryStore) Reserve(_ context.Context, executionID, owner string, accountIndex int64, apiKeyIndex uint8, changeNonce int64) (reservation, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	bound, exists := value.bindings[executionID]
	if exists && bound.Status == "revoked" {
		return reservation{}, errBindingRevoked
	}
	if exists && (bound.Status == "revocation_pending" || bound.Status == "revoking") {
		return reservation{}, errRotationOpen
	}
	if exists && (bound.OwnerAddress != owner || bound.AccountIndex != accountIndex || bound.APIKeyIndex != apiKeyIndex) {
		return reservation{}, errBindingMismatch
	}
	for otherExecutionID, other := range value.bindings {
		if otherExecutionID != executionID && other.AccountIndex == accountIndex {
			return reservation{}, errAccountBound
		}
	}
	for _, id := range value.versions[executionID] {
		record := value.records[id]
		status := record.Status
		if status == statusGenerating || status == statusPending || status == statusVerifying {
			if status != statusGenerating && record.ChangeNonce == changeNonce {
				return reservation{Credential: cloneCredential(record), Rotation: bound.ActiveCredentialID != "", Existing: true}, nil
			}
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
		Purpose:            purposeAssociation,
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

func (value *memoryStore) ReserveRevocation(_ context.Context, executionID string) (revocationReservation, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	bound, exists := value.bindings[executionID]
	if !exists || bound.ActiveCredentialID == "" {
		return revocationReservation{}, errNoActiveCredential
	}
	if bound.Status == "revoked" {
		return revocationReservation{}, errBindingRevoked
	}
	active := value.records[bound.ActiveCredentialID]
	if active.Status != statusLinked || active.Purpose != purposeAssociation {
		return revocationReservation{}, errNoActiveCredential
	}
	for _, id := range value.versions[executionID] {
		record := value.records[id]
		if record.Status != statusGenerating && record.Status != statusPending && record.Status != statusVerifying {
			continue
		}
		if record.Purpose == purposeRevocation && record.ReplacesCredentialID == active.ID &&
			record.Status != statusGenerating &&
			(bound.Status == "revocation_pending" || bound.Status == "revoking") {
			return revocationReservation{
				Active: active, Tombstone: cloneCredential(record), Existing: true,
			}, nil
		}
		return revocationReservation{}, errRotationOpen
	}
	if bound.Status != "linked" {
		return revocationReservation{}, errRotationOpen
	}
	id, err := newUUID()
	if err != nil {
		return revocationReservation{}, err
	}
	now := time.Now().UTC()
	tombstone := credential{
		ID:                   id,
		ExecutionAccountID:   executionID,
		OwnerAddress:         bound.OwnerAddress,
		AccountIndex:         bound.AccountIndex,
		APIKeyIndex:          bound.APIKeyIndex,
		Version:              int64(len(value.versions[executionID]) + 1),
		Purpose:              purposeRevocation,
		ReplacesCredentialID: active.ID,
		Status:               statusGenerating,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	bound.Status = "revocation_pending"
	value.bindings[executionID] = bound
	value.records[id] = tombstone
	value.versions[executionID] = append(value.versions[executionID], id)
	return revocationReservation{Active: cloneCredential(active), Tombstone: cloneCredential(tombstone)}, nil
}

func (value *memoryStore) ReplaceExpiredRevocation(_ context.Context, expired credential, now time.Time) (revocationReservation, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	canonical, exists := value.records[expired.ID]
	bound := value.bindings[expired.ExecutionAccountID]
	if !exists || canonical.Purpose != purposeRevocation || canonical.Status != statusPending ||
		canonical.ExpiresAtMS > now.UnixMilli() || bound.Status != "revocation_pending" ||
		bound.ActiveCredentialID != canonical.ReplacesCredentialID {
		return revocationReservation{}, errors.New("credential revocation is not replaceable")
	}
	active := value.records[bound.ActiveCredentialID]
	if active.Status != statusLinked || active.Purpose != purposeAssociation {
		return revocationReservation{}, errNoActiveCredential
	}
	zero(canonical.EncryptedDataKey)
	zero(canonical.CipherNonce)
	zero(canonical.Ciphertext)
	zero(canonical.AADDigest)
	canonical.EncryptedDataKey = nil
	canonical.CipherNonce = nil
	canonical.Ciphertext = nil
	canonical.AADDigest = nil
	canonical.KMSKeyID = ""
	canonical.Status = statusSuperseded
	canonical.UpdatedAt = now
	value.records[canonical.ID] = canonical

	id, err := newUUID()
	if err != nil {
		return revocationReservation{}, err
	}
	replacement := credential{
		ID:                   id,
		ExecutionAccountID:   expired.ExecutionAccountID,
		OwnerAddress:         active.OwnerAddress,
		AccountIndex:         active.AccountIndex,
		APIKeyIndex:          active.APIKeyIndex,
		Version:              int64(len(value.versions[expired.ExecutionAccountID]) + 1),
		Purpose:              purposeRevocation,
		ReplacesCredentialID: active.ID,
		Status:               statusGenerating,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	value.records[id] = replacement
	value.versions[expired.ExecutionAccountID] = append(value.versions[expired.ExecutionAccountID], id)
	return revocationReservation{
		Active: cloneCredential(active), Tombstone: cloneCredential(replacement),
	}, nil
}

func (value *memoryStore) Complete(_ context.Context, record credential) (credential, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	current, exists := value.records[record.ID]
	if !exists || current.Status != statusGenerating ||
		current.Purpose != purposeAssociation || record.Purpose != purposeAssociation {
		return credential{}, errNotFound
	}
	record.Status = statusPending
	record.CreatedAt = current.CreatedAt
	record.UpdatedAt = time.Now().UTC()
	value.records[record.ID] = cloneCredential(record)
	return cloneCredential(record), nil
}

func (value *memoryStore) CompleteRevocation(_ context.Context, record credential) (credential, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	current, exists := value.records[record.ID]
	if !exists || current.Status != statusGenerating || current.Purpose != purposeRevocation ||
		current.ReplacesCredentialID != record.ReplacesCredentialID {
		return credential{}, errNotFound
	}
	record.Status = statusPending
	record.CreatedAt = current.CreatedAt
	record.UpdatedAt = time.Now().UTC()
	value.records[record.ID] = cloneCredential(record)
	return cloneCredential(record), nil
}

func (value *memoryStore) VerifyRevocationNonce(_ context.Context, active credential, venueNonce int64) error {
	value.mu.Lock()
	defer value.mu.Unlock()
	bound := value.bindings[active.ExecutionAccountID]
	if venueNonce < 0 || bound.Status != "revocation_pending" ||
		bound.ActiveCredentialID != active.ID {
		return errors.New("Lighter signing state is not safe for revocation")
	}
	prefix := active.ID + ":"
	for key, request := range value.signing {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		var nonce int64
		if _, err := fmt.Sscanf(strings.TrimPrefix(key, prefix), "%d", &nonce); err != nil ||
			request.result == nil || nonce >= venueNonce {
			return errors.New("Lighter signing state is not safe for revocation")
		}
	}
	return nil
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
	if current.Status != statusPending ||
		current.Purpose != purposeAssociation || record.Purpose != purposeAssociation {
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

func (value *memoryStore) MarkRevocationVerifying(_ context.Context, record credential, signature string, txInfo []byte) (credential, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	current := value.records[record.ID]
	if current.Status != statusPending || current.Purpose != purposeRevocation {
		return credential{}, errors.New("invalid transition")
	}
	bound := value.bindings[record.ExecutionAccountID]
	if bound.Status != "revocation_pending" || bound.ActiveCredentialID != record.ReplacesCredentialID {
		return credential{}, errors.New("invalid revocation binding")
	}
	current.Status = statusVerifying
	current.L1Signature = signature
	current.TxInfo = append([]byte(nil), txInfo...)
	current.UpdatedAt = time.Now().UTC()
	value.records[record.ID] = current
	bound.Status = "revoking"
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

func (value *memoryStore) FinalizeRevocation(_ context.Context, record credential, registeredPublicKey string) (credential, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	current, exists := value.records[record.ID]
	if !exists || current.Status != statusVerifying || current.Purpose != purposeRevocation ||
		normalizePublicKey(current.PublicKey) != normalizePublicKey(registeredPublicKey) {
		return credential{}, errors.New("credential revocation proof mismatch")
	}
	bound := value.bindings[record.ExecutionAccountID]
	if bound.Status != "revoking" || bound.ActiveCredentialID != current.ReplacesCredentialID {
		return credential{}, errors.New("credential revocation binding changed")
	}
	active := value.records[bound.ActiveCredentialID]
	if active.Status != statusLinked ||
		normalizePublicKey(active.PublicKey) == normalizePublicKey(registeredPublicKey) {
		return credential{}, errors.New("registered Lighter key did not change")
	}
	for _, id := range value.versions[record.ExecutionAccountID] {
		candidate := value.records[id]
		zero(candidate.EncryptedDataKey)
		zero(candidate.CipherNonce)
		zero(candidate.Ciphertext)
		zero(candidate.AADDigest)
		candidate.EncryptedDataKey = nil
		candidate.CipherNonce = nil
		candidate.Ciphertext = nil
		candidate.AADDigest = nil
		candidate.KMSKeyID = ""
		candidate.Status = statusRevoked
		candidate.UpdatedAt = time.Now().UTC()
		if id == record.ID {
			candidate.RegisteredPublicKey = normalizePublicKey(registeredPublicKey)
			current = candidate
		}
		value.records[id] = candidate
	}
	bound.Status = "revoked"
	bound.ActiveCredentialID = ""
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

func (value *memoryStore) ExpectedNonce(_ context.Context, record credential) (uint64, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	bound, exists := value.bindings[record.ExecutionAccountID]
	if !exists || bound.Status != "linked" || bound.ActiveCredentialID != record.ID || record.ChangeNonce < 0 {
		return 0, errors.New("execution account credential changed during observation")
	}
	if next, exists := value.nextNonces[record.ID]; exists {
		return next, nil
	}
	return uint64(record.ChangeNonce) + 1, nil
}

func (value *memoryStore) ClaimSigningNonce(_ context.Context, record credential, intentID string, nonce uint64, digest string) (*signedTransaction, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	bound, exists := value.bindings[record.ExecutionAccountID]
	if !exists || bound.Status != "linked" || bound.ActiveCredentialID != record.ID || record.ChangeNonce < 0 {
		return nil, errors.New("execution account credential changed during nonce claim")
	}
	key := fmt.Sprintf("%s:%d", record.ID, nonce)
	if existing, exists := value.signing[key]; exists {
		if existing.intentID != intentID || existing.digest != digest {
			return nil, errors.New("Lighter signing nonce is already bound to another request")
		}
		if existing.result == nil {
			return nil, errors.New("Lighter signing request is claimed but has no durable result")
		}
		copy := *existing.result
		copy.TxInfo = append([]byte(nil), existing.result.TxInfo...)
		return &copy, nil
	}
	for existingKey, existing := range value.signing {
		if strings.HasPrefix(existingKey, record.ID+":") && existing.result == nil {
			return nil, errors.New("Lighter signing request is claimed but has no durable result")
		}
	}
	expected, exists := value.nextNonces[record.ID]
	if !exists {
		expected = uint64(record.ChangeNonce) + 1
	}
	if nonce != expected {
		return nil, fmt.Errorf("Lighter signing nonce must equal %d", expected)
	}
	value.signing[key] = memorySigningRequest{intentID: intentID, digest: digest}
	value.nextNonces[record.ID] = expected + 1
	return nil, nil
}

func (value *memoryStore) CompleteSigningRequest(_ context.Context, record credential, intentID string, nonce uint64,
	digest string, signed signedTransaction) error {
	value.mu.Lock()
	defer value.mu.Unlock()
	key := fmt.Sprintf("%s:%d", record.ID, nonce)
	request, exists := value.signing[key]
	if !exists || request.intentID != intentID || request.digest != digest || request.result != nil ||
		validateSignedResult(record, intentID, signed.TxType, signed) != nil {
		return errors.New("complete Lighter signing request")
	}
	copy := signed
	copy.TxInfo = append([]byte(nil), signed.TxInfo...)
	request.result = &copy
	value.signing[key] = request
	return nil
}

func (value *memoryStore) VerifyActive(_ context.Context, record credential) error {
	value.mu.Lock()
	defer value.mu.Unlock()
	bound, exists := value.bindings[record.ExecutionAccountID]
	if !exists || bound.Status != "linked" || bound.ActiveCredentialID != record.ID || value.records[record.ID].Status != statusLinked {
		return errors.New("execution account credential changed during observation")
	}
	return nil
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

func (value *memoryStore) AuditActive(_ context.Context, record credential, _ string, _ map[string]any) error {
	value.mu.Lock()
	defer value.mu.Unlock()
	bound, exists := value.bindings[record.ExecutionAccountID]
	if !exists || bound.Status != "linked" || bound.ActiveCredentialID != record.ID {
		return errors.New("execution account credential changed during signing")
	}
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
	mu                   sync.Mutex
	generated            int
	discoveredAccount    int64
	discoveryErr         error
	discoveryCalls       int
	nextNonce            int64
	nonceErr             error
	nonceCalls           int
	recoveredOwner       string
	registered           string
	broadcastErr         error
	broadcasts           int
	observedAccountIndex int64
	observeErr           error
}

func (value *fakeLighter) DiscoverEmptySubaccount(_ context.Context, _ string) (int64, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	value.discoveryCalls++
	if value.discoveryErr != nil {
		return 0, value.discoveryErr
	}
	if value.discoveredAccount == 0 {
		return 42, nil
	}
	return value.discoveredAccount, nil
}

func (value *fakeLighter) NextNonce(_ context.Context, _ int64, _ uint8) (int64, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	value.nonceCalls++
	if value.nonceErr != nil {
		return 0, value.nonceErr
	}
	if value.nextNonce == 0 {
		return 7, nil
	}
	return value.nextNonce, nil
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

func (value *fakeLighter) ObserveAccount(_ context.Context, _ string, accountIndex int64, apiKeyIndex uint8, marketID uint16, expectedNonce uint64) (lighterAccountState, error) {
	if value.observeErr != nil {
		return lighterAccountState{}, value.observeErr
	}
	if value.observedAccountIndex != 0 {
		accountIndex = value.observedAccountIndex
	}
	return lighterAccountState{
		AccountIndex: uint64(accountIndex), APIKeyIndex: apiKeyIndex, MarketID: marketID,
		Nonce: expectedNonce, ExpectedNonce: expectedNonce, CollateralRaw: "100",
		MaintenanceRequirementRaw: "25", MaintenanceMarginRatioMicros: 4_000_000,
		NoUnknownOrders: true, NoUnknownPositions: true, Flat: true, RESTReconstructed: true,
		StateDigest: strings.Repeat("a", 64), ObservedAt: time.Unix(2_000_000_000, 0).UTC(),
	}, nil
}

func (*fakeLighter) SignCreateOrder(_ string, accountIndex int64, apiKeyIndex uint8, request createOrderRequest) (signedTransaction, error) {
	return fakeSignedTransaction(accountIndex, apiKeyIndex, request.TransactOptions.Nonce, txtypes.TxTypeL2CreateOrder), nil
}

func (*fakeLighter) SignCancelOrder(_ string, accountIndex int64, apiKeyIndex uint8, request cancelOrderRequest) (signedTransaction, error) {
	return fakeSignedTransaction(accountIndex, apiKeyIndex, request.TransactOptions.Nonce, txtypes.TxTypeL2CancelOrder), nil
}

func (*fakeLighter) SignCancelAll(_ string, accountIndex int64, apiKeyIndex uint8, request cancelAllRequest) (signedTransaction, error) {
	return fakeSignedTransaction(accountIndex, apiKeyIndex, request.TransactOptions.Nonce, txtypes.TxTypeL2CancelAllOrders), nil
}

func fakeSignedTransaction(accountIndex int64, apiKeyIndex uint8, nonce int64, txType uint8) signedTransaction {
	return signedTransaction{
		TxType: txType,
		TxHash: fmt.Sprintf("0x%064x", nonce),
		TxInfo: []byte(fmt.Sprintf(`{"AccountIndex":%d,"ApiKeyIndex":%d,"Nonce":%d}`, accountIndex, apiKeyIndex, nonce)),
	}
}

func validTestSignature() string {
	return "0x" + strings.Repeat("11", 65)
}
