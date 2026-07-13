package restrictctl

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
)

func LoadKeyPair(privatePath, publicPath string) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	privateBody, err := readPrivateKey(privatePath)
	if err != nil {
		return nil, nil, err
	}
	publicBody, err := os.ReadFile(publicPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read trusted public key: %w", err)
	}
	privateBlock, rest := pem.Decode(privateBody)
	if privateBlock == nil || privateBlock.Type != "PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, nil, errors.New("private key must contain one PKCS#8 PEM block")
	}
	parsedPrivate, err := x509.ParsePKCS8PrivateKey(privateBlock.Bytes)
	if err != nil {
		return nil, nil, errors.New("parse Ed25519 private key")
	}
	privateKey, ok := parsedPrivate.(ed25519.PrivateKey)
	if !ok {
		return nil, nil, errors.New("private key must be Ed25519")
	}
	publicBlock, rest := pem.Decode(publicBody)
	if publicBlock == nil || publicBlock.Type != "PUBLIC KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, nil, errors.New("trusted public key must contain one PKIX PEM block")
	}
	parsedPublic, err := x509.ParsePKIXPublicKey(publicBlock.Bytes)
	if err != nil {
		return nil, nil, errors.New("parse trusted Ed25519 public key")
	}
	publicKey, ok := parsedPublic.(ed25519.PublicKey)
	if !ok {
		return nil, nil, errors.New("trusted public key must be Ed25519")
	}
	derived := privateKey.Public().(ed25519.PublicKey)
	if !bytes.Equal(derived, publicKey) {
		return nil, nil, errors.New("signing key does not match the trusted public key")
	}
	return privateKey, publicKey, nil
}

func readPrivateKey(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect private key: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("private key must be a regular mode-0600 file")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}
	return body, nil
}
