package publisher

import "testing"

func TestConfigRejectsCrossTenantBindings(t *testing.T) {
	first := configTestAccount("10000000-0000-4000-8000-000000000001", 77, "0x1111111111111111111111111111111111111111", "/secrets/token-1")
	second := configTestAccount("20000000-0000-4000-8000-000000000002", 77, "0x2222222222222222222222222222222222222222", "/secrets/token-2")
	config := Config{LighterURL: lighterMainnetURL, Accounts: []AccountBinding{first, second}}
	if err := validateConfig(config); err == nil {
		t.Fatal("duplicate Lighter account must be rejected")
	}
	second.Lighter.AccountIndex = 78
	second.Robinhood.Vault = first.Robinhood.Vault
	config.Accounts[1] = second
	if err := validateConfig(config); err == nil {
		t.Fatal("duplicate vault must be rejected")
	}
}

func TestOnlySingletonMayOmitProductReadiness(t *testing.T) {
	account := configTestAccount("10000000-0000-4000-8000-000000000001", 77, "0x1111111111111111111111111111111111111111", "/secrets/token-1")
	account.ReadinessAccountID = ""
	if err := validateConfig(Config{LighterURL: lighterMainnetURL, Accounts: []AccountBinding{account}}); err == nil {
		t.Fatal("user account must publish product readiness")
	}
	account.ExecutionAccountID = "singleton-mainnet-canary"
	if err := validateConfig(Config{LighterURL: lighterMainnetURL, Accounts: []AccountBinding{account}}); err != nil {
		t.Fatalf("singleton should be accepted: %v", err)
	}
}

func configTestAccount(id string, lighterIndex uint64, vault, token string) AccountBinding {
	return AccountBinding{
		ExecutionAccountID: id, ReadinessAccountID: id,
		Lighter: LighterBinding{
			AccountIndex: lighterIndex, APIKeyIndex: 4, MarketID: 5, ReadOnlyTokenFile: token,
			ExpectedNonceFile: "/state/nonce", MinimumCollateralRaw: "50",
		},
		Robinhood: RobinhoodBinding{
			Registry: "0x3333333333333333333333333333333333333333", Factory: "0x4444444444444444444444444444444444444444",
			Vault: vault, RiskManager: "0x5555555555555555555555555555555555555555", SpotAdapter: "0x6666666666666666666666666666666666666666",
			Owner: "0x7777777777777777777777777777777777777777", Signer: "0x8888888888888888888888888888888888888888",
			VaultCodeHash:        "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			MinimumSettlementRaw: "25000000", MinimumOwnerGasRaw: "1", MinimumSignerGasRaw: "1", ReceiptJournalFile: "/state/receipts",
		},
	}
}
