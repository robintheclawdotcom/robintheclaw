package main

import "testing"

func TestDisabledConfigurationHasNoSecretDependencies(t *testing.T) {
	t.Setenv("ROBINHOOD_PROVISIONER_ENABLED", "false")
	configuration, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if configuration.Enabled {
		t.Fatal("disabled provisioner was enabled")
	}
	if !configuration.RunMigrations {
		t.Fatal("development migrations must default on")
	}
}

func TestProvisionerRejectsSharedBridgeKey(t *testing.T) {
	t.Setenv("ROBINHOOD_PROVISIONER_ENABLED", "true")
	t.Setenv("ROBINHOOD_PROVISIONER_HMAC_KEY", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	t.Setenv("ROBINHOOD_SIGNER_BRIDGE_HMAC_KEY", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if _, err := loadConfig(); err == nil {
		t.Fatal("shared product and signer HMAC key was accepted")
	}
}

func TestKMSControlPlaneARNMustPinPublishedVersion(t *testing.T) {
	base := "arn:aws:lambda:us-east-1:123456789012:function:robinhood-key-control-plane"
	valid := base + ":17"
	if !validLambdaVersionARN(valid) {
		t.Fatal("valid function version ARN rejected")
	}
	for _, value := range []string{
		base,
		base + ":live",
		base + ":$LATEST",
		base + ":0",
		base + ":01",
		"arn:aws:lambda:us-east-1:123456789012:function:*:1",
		"arn:aws:lambda:us-east-1:123456789012:layer:robinhood-key-control-plane:1",
		"arn:aws:lambda:us-east-1:12345678901:function:robinhood-key-control-plane:1",
	} {
		if validLambdaVersionARN(value) {
			t.Fatalf("unsafe function ARN accepted: %s", value)
		}
	}
}

func TestProvisionerMigrationModeIsStrict(t *testing.T) {
	t.Setenv("ROBINHOOD_PROVISIONER_ENABLED", "true")
	t.Setenv("ROBINHOOD_PROVISIONER_RUN_MIGRATIONS", "FALSE")
	if _, err := loadConfig(); err == nil {
		t.Fatal("non-canonical migration mode was accepted")
	}
}
