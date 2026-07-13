package publisher

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReceiptJournalRejectsVaultSubstitution(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "receipts.json")
	if err := os.WriteFile(path, []byte(`{"vault":"0x1111111111111111111111111111111111111111","hashes":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	binding := RobinhoodBinding{
		Vault:              "0x2222222222222222222222222222222222222222",
		ReceiptJournalFile: path,
	}
	if _, err := loadReceiptJournal(binding); err == nil {
		t.Fatal("expected cross-account receipt journal substitution to fail")
	}
}
