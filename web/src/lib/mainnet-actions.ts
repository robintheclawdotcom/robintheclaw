import type { AgentCommandRecord, ExecutionBindingRecord } from "./app-types";

export function canonicalDeploymentAction(binding: ExecutionBindingRecord) {
  const action = binding.robinhoodDeploymentAction;
  const owner = binding.ownerAddress.toLowerCase();
  const factory = binding.robinhoodFactoryAddress?.toLowerCase();
  if (!action || !/^0x[0-9a-f]{40}$/.test(owner) || !factory) return false;
  const expectedData = `0x4c96a389${"0".repeat(24)}${owner.slice(2)}`;
  return action.kind === "deploy_user_graph"
    && action.chainId === "4663"
    && action.value === "0"
    && action.to.toLowerCase() === factory
    && action.data.toLowerCase() === expectedData;
}

export function canonicalOwnerActionSet(
  actions: AgentCommandRecord["ownerActions"],
  owner: string,
  vault: string,
) {
  if (actions.length < 1 || actions.length > 2) return false;
  if (!actions.every((action) => action.chain_id === 4663
    && action.value === "0"
    && action.from.toLowerCase() === owner.toLowerCase()
    && action.to.toLowerCase() === vault.toLowerCase()
  )) return false;
  if (!/^0x142834dd[0-9a-fA-F]{64}$/.test(actions.at(-1)!.data)) return false;
  return actions.length === 1 || actions[0].data.toLowerCase() === "0x51755334";
}
