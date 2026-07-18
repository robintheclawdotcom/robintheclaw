package evaluation

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

const strategyPolicyCommitmentSHA256 = "39f82d4576fbd3d1e230a9d39d1971d9e6a0e004137eefd0b4cc700934728cf9"

var expectedStrategyPolicyCommitment = strategyPolicyCommitmentSHA256

func verifyStrategyPolicy(minimumNetEdgePPM uint64, saltHex string) error {
	if minimumNetEdgePPM == 0 || minimumNetEdgePPM > 1_000_000 {
		return errors.New("AAPL minimum net edge is outside the approved range")
	}
	salt, err := hex.DecodeString(saltHex)
	if err != nil || len(salt) != 32 || hex.EncodeToString(salt) != saltHex {
		return errors.New("AAPL_STRATEGY_POLICY_SALT must be 32-byte lowercase hex")
	}
	canonical := fmt.Sprintf(
		"basis-aapl-v1\x00minimum_net_edge_ppm\x00%d\x00%s",
		minimumNetEdgePPM,
		saltHex,
	)
	actual := fmt.Sprintf("%x", sha256.Sum256([]byte(canonical)))
	if actual != expectedStrategyPolicyCommitment {
		return errors.New("AAPL strategy policy commitment mismatch")
	}
	return nil
}
