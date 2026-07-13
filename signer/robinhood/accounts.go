package main

import (
	"context"
	"errors"
	"sync"
)

type accountWriterManager struct {
	base     Config
	client   chainClient
	verifier chainClient
	kms      kmsAPI
	resolver accountResolver
	ctx      context.Context
	mu       sync.Mutex
	writers  map[string]managedWriter
}

type managedWriter struct {
	deploymentID string
	writer       *Writer
	journal      *Journal
}

func newAccountWriterManager(ctx context.Context, base Config, client, verifier chainClient, kms kmsAPI, resolver accountResolver) *accountWriterManager {
	return &accountWriterManager{
		base:     base,
		client:   client,
		verifier: verifier,
		kms:      kms,
		resolver: resolver,
		ctx:      ctx,
		writers:  make(map[string]managedWriter),
	}
}

func (value *accountWriterManager) writer(ctx context.Context, executionID string) (*Writer, error) {
	binding, err := value.resolver.Resolve(ctx, executionID)
	if err != nil {
		return nil, err
	}
	account, err := binding.accountConfig(value.base)
	if err != nil {
		return nil, err
	}
	manifest, deploymentID := account.manifest()
	value.mu.Lock()
	defer value.mu.Unlock()
	if existing, ok := value.writers[executionID]; ok {
		if existing.deploymentID != deploymentID {
			existing.writer.ready.Store(false)
			return nil, errors.New("execution key or graph changed; signer restart and reconciliation required")
		}
		return existing.writer, nil
	}
	signer, err := newKMSSigner(ctx, value.kms, account.KMSKeyID)
	if err != nil {
		return nil, err
	}
	if signer.Address() != account.SignerAddress {
		return nil, errors.New("resolved KMS key does not match execution account signer")
	}
	journal, err := openJournal(ctx, account.DatabaseURL, manifest, deploymentID)
	if err != nil {
		return nil, err
	}
	writer := newWriter(account, value.client, value.verifier, signer, journal)
	if err := writer.Recover(ctx); err != nil {
		journal.Close()
		return nil, err
	}
	value.writers[executionID] = managedWriter{deploymentID: deploymentID, writer: writer, journal: journal}
	go writer.RunReconciler(value.ctx)
	return writer, nil
}

func (value *accountWriterManager) close() {
	value.mu.Lock()
	defer value.mu.Unlock()
	for executionID, managed := range value.writers {
		managed.writer.ready.Store(false)
		managed.journal.Close()
		delete(value.writers, executionID)
	}
}

func (value *accountWriterManager) ready(ctx context.Context) bool {
	return value.resolver.Ready(ctx)
}
