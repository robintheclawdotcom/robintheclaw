package main

import (
	"strings"
	"testing"
)

func TestProvisionerIsDisabledByDefault(t *testing.T) {
	t.Setenv("LIGHTER_PROVISIONER_ENABLED", "false")
	value, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if value.Enabled {
		t.Fatal("provisioner enabled without explicit flag")
	}
	if !value.RunMigrations {
		t.Fatal("development migration default changed")
	}
}

func TestEnabledProvisionerUsesPrivateDatabaseConfiguration(t *testing.T) {
	t.Setenv("LIGHTER_PROVISIONER_ENABLED", "true")
	t.Setenv("LIGHTER_PROVISIONER_HMAC_KEY", strings.Repeat("42", 32))
	t.Setenv("PROVISIONER_CALLER_ID", "product-api")
	t.Setenv("LIGHTER_SIGNER_BRIDGE_HMAC_KEY", strings.Repeat("24", 32))
	t.Setenv("LIGHTER_SIGNER_BRIDGE_CALLER_ID", "lighter-signer")
	t.Setenv("LIGHTER_PUBLISHER_BRIDGE_HMAC_KEY", strings.Repeat("66", 32))
	t.Setenv("LIGHTER_PUBLISHER_BRIDGE_CALLER_ID", "account-publisher")
	t.Setenv("LIGHTER_PROVISIONER_DATABASE_URL", "postgres://provisioner.invalid/credentials")
	t.Setenv("AWS_KMS_KEY_ID", "alias/lighter")
	t.Setenv("LIGHTER_CHAIN_ID", "300")
	t.Setenv("LIGHTER_PUBLISHER_MARKET_ID", "5")
	t.Setenv("LIGHTER_AAPL_BASE_DECIMALS", "4")
	t.Setenv("LIGHTER_AAPL_PRICE_DECIMALS", "2")
	value, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if value.DatabaseURL != "postgres://provisioner.invalid/credentials" {
		t.Fatalf("database URL = %q", value.DatabaseURL)
	}
}

func TestProvisionerCanRunWithRestrictedRuntimeDatabaseRole(t *testing.T) {
	t.Setenv("LIGHTER_PROVISIONER_ENABLED", "false")
	t.Setenv("LIGHTER_PROVISIONER_RUN_MIGRATIONS", "false")
	value, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if value.RunMigrations {
		t.Fatal("runtime migration opt-out was ignored")
	}
}

func TestProvisionerRejectsAmbiguousMigrationFlag(t *testing.T) {
	t.Setenv("LIGHTER_PROVISIONER_ENABLED", "false")
	t.Setenv("LIGHTER_PROVISIONER_RUN_MIGRATIONS", "0")
	if _, err := loadConfig(); err == nil {
		t.Fatal("ambiguous migration flag was accepted")
	}
}
