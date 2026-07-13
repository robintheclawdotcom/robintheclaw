"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import type { DashboardSnapshot } from "../lib/app-types";
import { depositCalls, mandateCall, parseTokenAmount, withdrawalCall } from "../lib/strategy-calls";
import { formatAddress } from "../lib/format";
import { useAppApi, useRobinAuth, useSmartWallet } from "./app-providers";
import { ErrorNotice } from "./app-ui";

export function MandateButton({ dashboard }: { dashboard: DashboardSnapshot }) {
  const smartWallet = useSmartWallet();
  const queryClient = useQueryClient();
  const vault = dashboard.vault;
  const mutation = useMutation({
    mutationFn: async () => {
      if (!vault || !dashboard.policyId) throw new Error("Strategy controls are not configured.");
      return smartWallet.executeCalls([mandateCall(vault.record.guardAddress, !vault.halted)], dashboard.policyId);
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["dashboard"] }),
  });

  if (!vault) return null;
  return (
    <div className="inline-action">
      <button className="button button-primary" disabled={mutation.isPending || smartWallet.pending} onClick={() => mutation.mutate()}>
        {mutation.isPending ? "Submitting…" : vault.halted ? "Start strategy" : "Pause strategy"}
      </button>
      {mutation.error && <span className="field-error" role="alert">{mutation.error.message}</span>}
    </div>
  );
}

export function WithdrawForm({ dashboard }: { dashboard: DashboardSnapshot }) {
  const smartWallet = useSmartWallet();
  const auth = useRobinAuth();
  const queryClient = useQueryClient();
  const [amount, setAmount] = useState("");
  const vault = dashboard.vault;
  const mutation = useMutation({
    mutationFn: async () => {
      if (!vault || !dashboard.policyId || !auth.embeddedAddress) throw new Error("Withdrawal is not available.");
      const raw = parseTokenAmount(amount, vault.balance.decimals);
      if (raw > BigInt(vault.balance.raw)) throw new Error("Amount exceeds the vault balance.");
      return smartWallet.executeCalls([
        withdrawalCall(vault.record.vaultAddress, auth.embeddedAddress, raw),
      ], dashboard.policyId);
    },
    onSuccess: () => {
      setAmount("");
      void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
    },
  });

  if (!vault) return null;
  return (
    <form className="action-form" id="withdraw" onSubmit={(event) => { event.preventDefault(); mutation.mutate(); }}>
      <label htmlFor="withdraw-amount">Amount to withdraw</label>
      <div className="amount-input"><input id="withdraw-amount" inputMode="decimal" value={amount} onChange={(event) => setAmount(event.target.value)} placeholder="0.00" /><span>{vault.balance.symbol}</span></div>
      <small>Funds return to your Robin strategy account at {formatAddress(auth.embeddedAddress)}.</small>
      <button className="button button-secondary" disabled={mutation.isPending || !amount} type="submit">{mutation.isPending ? "Withdrawing…" : "Withdraw"}</button>
      {mutation.error && <span className="field-error" role="alert">{mutation.error.message}</span>}
    </form>
  );
}

export function AddFundsForm({ dashboard }: { dashboard: DashboardSnapshot }) {
  const api = useAppApi();
  const auth = useRobinAuth();
  const smartWallet = useSmartWallet();
  const queryClient = useQueryClient();
  const { data: me } = useQuery({ queryKey: ["me"], queryFn: () => api.me() });
  const vault = dashboard.vault;
  const linkedAddresses = new Set(auth.accounts.map((wallet) => wallet.address.toLowerCase()));
  const eligible = me?.wallets.filter((wallet) => linkedAddresses.has(wallet.address.toLowerCase())) ?? [];
  const [wallet, setWallet] = useState("");
  const [amount, setAmount] = useState("");

  useEffect(() => {
    if (!wallet && eligible.length) setWallet(me?.preferences.activeFundingWallet ?? eligible[0].address);
  }, [eligible, me?.preferences.activeFundingWallet, wallet]);

  const mutation = useMutation({
    mutationFn: async () => {
      if (!vault || !dashboard.policyId || !wallet) throw new Error("Choose a connected funding wallet.");
      const raw = parseTokenAmount(amount, vault.balance.decimals);
      return smartWallet.executeCalls(
        depositCalls(vault.record.assetAddress, vault.record.vaultAddress, raw),
        dashboard.policyId,
        wallet,
      );
    },
    onSuccess: () => {
      setAmount("");
      void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
    },
  });

  if (!vault) return null;
  return (
    <form className="action-form" id="fund" onSubmit={(event) => { event.preventDefault(); mutation.mutate(); }}>
      <label htmlFor="funding-wallet">Funding wallet</label>
      <select id="funding-wallet" value={wallet} onChange={(event) => setWallet(event.target.value)}>
        {!eligible.length && <option value="">No connected wallet</option>}
        {eligible.map((item) => <option value={item.address} key={item.address}>{item.label ?? item.walletType} · {formatAddress(item.address)}</option>)}
      </select>
      <label htmlFor="deposit-amount">Amount to add</label>
      <div className="amount-input"><input id="deposit-amount" inputMode="decimal" value={amount} onChange={(event) => setAmount(event.target.value)} placeholder="0.00" /><span>{vault.balance.symbol}</span></div>
      <small>The selected wallet signs one sponsored approval-and-deposit batch.</small>
      <button className="button button-primary" disabled={mutation.isPending || !amount || !wallet} type="submit">{mutation.isPending ? "Adding funds…" : "Add funds"}</button>
      {mutation.error && <ErrorNotice error={mutation.error} />}
    </form>
  );
}
