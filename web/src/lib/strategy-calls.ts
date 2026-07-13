import { encodeFunctionData, parseAbi } from "viem";
import type { TransactionCall } from "./app-types";

const guardAbi = parseAbi(["function setHalted(bool halted)"]);
const vaultAbi = parseAbi([
  "function deposit(uint256 amount)",
  "function withdraw(address to,uint256 amount)",
]);
const tokenAbi = parseAbi(["function approve(address spender,uint256 amount) returns (bool)"]);

export function mandateCall(guard: `0x${string}`, halted: boolean): TransactionCall {
  return {
    to: guard,
    data: encodeFunctionData({ abi: guardAbi, functionName: "setHalted", args: [halted] }),
    value: "0",
  };
}

export function withdrawalCall(vault: `0x${string}`, recipient: `0x${string}`, amount: bigint): TransactionCall {
  return {
    to: vault,
    data: encodeFunctionData({ abi: vaultAbi, functionName: "withdraw", args: [recipient, amount] }),
    value: "0",
  };
}

export function depositCalls(asset: `0x${string}`, vault: `0x${string}`, amount: bigint): TransactionCall[] {
  return [
    {
      to: asset,
      data: encodeFunctionData({ abi: tokenAbi, functionName: "approve", args: [vault, amount] }),
      value: "0",
    },
    {
      to: vault,
      data: encodeFunctionData({ abi: vaultAbi, functionName: "deposit", args: [amount] }),
      value: "0",
    },
  ];
}

export function parseTokenAmount(value: string, decimals: number): bigint {
  const trimmed = value.trim();
  if (!/^\d+(\.\d+)?$/.test(trimmed)) throw new Error("Enter a valid positive amount.");
  const [whole, fraction = ""] = trimmed.split(".");
  if (fraction.length > decimals) throw new Error(`Use no more than ${decimals} decimal places.`);
  const raw = `${whole}${fraction.padEnd(decimals, "0")}`.replace(/^0+(?=\d)/, "");
  const amount = BigInt(raw || "0");
  if (amount <= 0n) throw new Error("Amount must be greater than zero.");
  return amount;
}
