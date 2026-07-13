import { describe, expect, it } from "vitest";
import type { ExecutionBindingRecord, OwnerAction } from "./app-types";
import { canonicalDeploymentAction, canonicalOwnerActionSet } from "./mainnet-actions";

const owner = "0x1111111111111111111111111111111111111111";
const factory = "0x2222222222222222222222222222222222222222";
const vault = "0x3333333333333333333333333333333333333333";

function deployment(): ExecutionBindingRecord {
  return {
    bindingRef: "binding",
    requestId: "request",
    providerRequestId: "account",
    venue: "robinhood",
    ownerAddress: owner,
    lighterAccountIndex: null,
    lighterApiKeyIndex: null,
    robinhoodVaultAddress: vault,
    robinhoodSignerAddress: "0x4444444444444444444444444444444444444444",
    robinhoodKeyVersion: 1,
    robinhoodFactoryAddress: factory,
    robinhoodRegistryAddress: "0x5555555555555555555555555555555555555555",
    robinhoodPolicyDigest: `0x${"12".repeat(32)}`,
    robinhoodRiskManagerAddress: "0x6666666666666666666666666666666666666666",
    robinhoodSpotAdapterAddress: "0x7777777777777777777777777777777777777777",
    robinhoodDeploymentBlock: null,
    robinhoodDeploymentAction: {
      kind: "deploy_user_graph",
      chainId: "4663",
      to: factory,
      data: `0x4c96a389${"0".repeat(24)}${owner.slice(2)}`,
      value: "0",
    },
    publicIdentifier: vault,
    publicKey: null,
    associationPayload: null,
    proofTransactionHash: null,
    status: "awaiting_signature",
    createdAt: "2026-07-13T00:00:00Z",
    updatedAt: "2026-07-13T00:00:00Z",
  };
}

describe("mainnet owner actions", () => {
  it("accepts only the canonical factory deployment", () => {
    const binding = deployment();
    expect(canonicalDeploymentAction(binding)).toBe(true);
    expect(canonicalDeploymentAction({
      ...binding,
      robinhoodDeploymentAction: { ...binding.robinhoodDeploymentAction!, to: vault },
    })).toBe(false);
    expect(canonicalDeploymentAction({
      ...binding,
      robinhoodDeploymentAction: { ...binding.robinhoodDeploymentAction!, data: "0x4c96a389" },
    })).toBe(false);
  });

  it("rejects withdrawal substitution and unexpected calldata", () => {
    const withdraw: OwnerAction = {
      chain_id: 4663,
      from: owner,
      to: vault,
      data: `0x142834dd${"0".repeat(63)}1`,
      value: "0",
    };
    expect(canonicalOwnerActionSet([withdraw], owner, vault)).toBe(true);
    expect(canonicalOwnerActionSet([{ ...withdraw, to: factory }], owner, vault)).toBe(false);
    expect(canonicalOwnerActionSet([{ ...withdraw, data: "0x51755334" }], owner, vault)).toBe(false);
  });

  it("accepts halt followed by withdrawal, in that order", () => {
    const halt: OwnerAction = {
      chain_id: 4663,
      from: owner,
      to: vault,
      data: "0x51755334",
      value: "0",
    };
    const withdraw: OwnerAction = {
      ...halt,
      data: `0x142834dd${"0".repeat(63)}1`,
    };
    expect(canonicalOwnerActionSet([halt, withdraw], owner, vault)).toBe(true);
    expect(canonicalOwnerActionSet([withdraw, halt], owner, vault)).toBe(false);
  });
});
