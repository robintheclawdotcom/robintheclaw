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
