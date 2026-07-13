package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"io"

	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
)

type kmsAPI interface {
	GenerateDataKey(context.Context, *awskms.GenerateDataKeyInput, ...func(*awskms.Options)) (*awskms.GenerateDataKeyOutput, error)
	Decrypt(context.Context, *awskms.DecryptInput, ...func(*awskms.Options)) (*awskms.DecryptOutput, error)
}

type envelope struct {
	kms   kmsAPI
	keyID string
	rand  io.Reader
}

func newEnvelope(kms kmsAPI, keyID string) *envelope {
	return &envelope{kms: kms, keyID: keyID, rand: rand.Reader}
}

func (value *envelope) seal(ctx context.Context, record credential, plaintext []byte) (sealedSecret, error) {
	output, err := value.kms.GenerateDataKey(ctx, &awskms.GenerateDataKeyInput{
		KeyId:             &value.keyID,
		KeySpec:           types.DataKeySpecAes256,
		EncryptionContext: encryptionContext(record),
	})
	if err != nil || output == nil || len(output.Plaintext) != 32 || len(output.CiphertextBlob) == 0 {
		if output != nil {
			zero(output.Plaintext)
		}
		return sealedSecret{}, errors.New("generate credential data key")
	}
	defer zero(output.Plaintext)

	block, err := aes.NewCipher(output.Plaintext)
	if err != nil {
		return sealedSecret{}, errors.New("initialize credential cipher")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return sealedSecret{}, errors.New("initialize credential envelope")
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(value.rand, nonce); err != nil {
		return sealedSecret{}, errors.New("generate credential nonce")
	}
	aad := aadFor(record)
	digest := sha256.Sum256(aad)
	return sealedSecret{
		EncryptedDataKey: append([]byte(nil), output.CiphertextBlob...),
		Nonce:            nonce,
		Ciphertext:       gcm.Seal(nil, nonce, plaintext, aad),
		AADDigest:        append([]byte(nil), digest[:]...),
		KMSKeyID:         value.keyID,
	}, nil
}

func (value *envelope) open(ctx context.Context, record credential) ([]byte, error) {
	aad := aadFor(record)
	digest := sha256.Sum256(aad)
	if len(record.AADDigest) != sha256.Size || subtle.ConstantTimeCompare(digest[:], record.AADDigest) != 1 {
		return nil, errors.New("credential binding mismatch")
	}
	if record.KMSKeyID != value.keyID {
		return nil, errors.New("credential KMS key mismatch")
	}
	output, err := value.kms.Decrypt(ctx, &awskms.DecryptInput{
		KeyId:             &value.keyID,
		CiphertextBlob:    record.EncryptedDataKey,
		EncryptionContext: encryptionContext(record),
	})
	if err != nil || output == nil || len(output.Plaintext) != 32 {
		if output != nil {
			zero(output.Plaintext)
		}
		return nil, errors.New("decrypt credential data key")
	}
	defer zero(output.Plaintext)
	block, err := aes.NewCipher(output.Plaintext)
	if err != nil {
		return nil, errors.New("initialize credential cipher")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(record.CipherNonce) != gcm.NonceSize() {
		return nil, errors.New("invalid credential envelope")
	}
	plaintext, err := gcm.Open(nil, record.CipherNonce, record.Ciphertext, aad)
	if err != nil {
		return nil, errors.New("open credential envelope")
	}
	return plaintext, nil
}

func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
