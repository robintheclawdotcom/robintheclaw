package main

import "testing"

func TestDisabledConfigurationHasNoSecretDependencies(t *testing.T) {
	t.Setenv("ROBINHOOD_SIGNER_ENABLED", "false")
	config, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Enabled {
		t.Fatal("disabled configuration was enabled")
	}
}

func TestPublicRPCIsRejected(t *testing.T) {
	if err := validatePrivateRPC("https://rpc.mainnet.chain.robinhood.com"); err == nil {
		t.Fatal("public RPC was accepted")
	}
	if err := validatePrivateRPC("http://internal.invalid"); err == nil {
		t.Fatal("plaintext RPC was accepted")
	}
	if err := validatePrivateRPC("https://authenticated.example.invalid/v2/token"); err != nil {
		t.Fatal(err)
	}
}

func TestStaticAccountConfigurationCannotEnableSigner(t *testing.T) {
	t.Setenv("ROBINHOOD_SIGNER_ENABLED", "true")
	t.Setenv("ROBINHOOD_SIGNER_HMAC_KEY", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	t.Setenv("SIGNER_CALLER_ID", "execution-coordinator")
	t.Setenv("DATABASE_URL", "postgres://unused")
	t.Setenv("ROBINHOOD_RPC_URL", "https://primary.example.invalid/token")
	t.Setenv("ROBINHOOD_RECONCILIATION_RPC_URL", "https://secondary.example.invalid/token")
	t.Setenv("ROBINHOOD_CHAIN_ID", "4663")
	t.Setenv("ROBINHOOD_SIGNER_ACCOUNTS_FILE", "/tmp/legacy.json")
	t.Setenv("ROBINHOOD_EXECUTION_ACCOUNT_ID", "11111111-1111-4111-8111-111111111111")
	t.Setenv("AWS_KMS_KEY_ID", "legacy-key")
	if _, err := loadConfig(); err == nil {
		t.Fatal("legacy static account configuration enabled the signer without a provisioner")
	}
}
