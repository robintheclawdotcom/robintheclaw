import type { AgentCommandRecord, ExecutionBindingRecord } from "./app-types";

export function canonicalDeploymentAction(binding: ExecutionBindingRecord) {
  const action = binding.robinhoodDeploymentAction;
  const owner = binding.ownerAddress.toLowerCase();
  const factory = binding.robinhoodFactoryAddress?.toLowerCase();
  if (!action || !/^0x[0-9a-f]{40}$/.test(owner) || !factory) return false;
  if (action.chainId !== "4663" || action.value !== "0") return false;
  if (action.kind === "deploy_user_graph") {
    const expectedData = `0x4c96a389${"0".repeat(24)}${owner.slice(2)}`;
    return action.to.toLowerCase() === factory && action.data.toLowerCase() === expectedData;
  }
  if (action.kind === "authorize_execution_agent") {
    const vault = binding.robinhoodVaultAddress?.toLowerCase();
    const signer = binding.robinhoodSignerAddress?.toLowerCase();
    if (!vault || !signer) return false;
    const expectedData = `0xa7d1c2a0${"0".repeat(24)}${signer.slice(2)}`;
    return action.to.toLowerCase() === vault && action.data.toLowerCase() === expectedData;
  }
  return false;
}

export function canonicalOwnerActionSet(
  actions: AgentCommandRecord["ownerActions"],
  owner: string,
  vault: string,
  command: AgentCommandRecord["command"],
) {
  if (actions.length < 1 || actions.length > 2) return false;
  if (!actions.every((action) => action.chain_id === 4663
    && action.value === "0"
    && action.from.toLowerCase() === owner.toLowerCase()
    && action.to.toLowerCase() === vault.toLowerCase()
  )) return false;
  if (command === "close") {
    return actions.length === 1 && actions[0].data.toLowerCase() === "0x51755334";
  }
  if (command !== "withdraw") return false;
  if (!/^0x142834dd[0-9a-fA-F]{64}$/.test(actions.at(-1)!.data)) return false;
  return actions.length === 1 || actions[0].data.toLowerCase() === "0x51755334";
}

export async function executeConfirmedOwnerActions(
  actions: AgentCommandRecord["ownerActions"],
  confirmedHashes: string[],
  execute: (action: AgentCommandRecord["ownerActions"][number]) => Promise<string>,
  onConfirmed: (hashes: string[]) => void,
) {
  const hashes = [...confirmedHashes];
  for (const [index, action] of actions.entries()) {
    if (hashes[index]) continue;
    const hash = await execute(action);
    if (!/^0x[0-9a-fA-F]{64}$/.test(hash)) {
      throw new Error("The wallet returned an invalid transaction hash.");
    }
    hashes[index] = hash;
    onConfirmed([...hashes]);
  }
  return hashes;
}
