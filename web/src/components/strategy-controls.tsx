"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import type { AgentStatus, DashboardSnapshot, ExecutionBindingRecord } from "../lib/app-types";
import { agentAction, agentStatusLabel } from "../lib/agent-lifecycle";
import { depositCalls, mandateCall, parseTokenAmount, withdrawalCall } from "../lib/strategy-calls";
import { formatAddress } from "../lib/format";
import { useAppApi, useRobinAuth, useSmartWallet } from "./app-providers";
import { ErrorNotice } from "./app-ui";

export function AgentButton({ dashboard }: { dashboard: DashboardSnapshot }) {
  const api = useAppApi();
  const queryClient = useQueryClient();
  const agent = dashboard.agent;
  const action = agentAction(agent);
  const mutation = useMutation<unknown, Error>({
    mutationFn: () => {
      if (!action) throw new Error("This lifecycle state has no manual transition.");
      if (action.kind === "create") return api.launchAgent();
      if (!agent) throw new Error("Agent is not available.");
      if (action.kind === "provision") return api.createExecutionAccount(agent.id);
      if (action.kind === "paper") return api.updatePaperAgent(agent.id, action.status);
      return api.agentCommand(agent.id, action.command);
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
      void queryClient.invalidateQueries({ queryKey: ["agent-readiness"] });
    },
  });

  if (!action) return null;
  return (
    <div className="inline-action">
      <button className="button button-primary" disabled={mutation.isPending} onClick={() => mutation.mutate()}>
        {mutation.isPending ? "Updating…" : action.label}
      </button>
      {mutation.error && <span className="field-error" role="alert">{mutation.error.message}</span>}
    </div>
  );
}

export function MainnetReadinessPanel({ dashboard }: { dashboard: DashboardSnapshot }) {
  const api = useAppApi();
  const auth = useRobinAuth();
  const queryClient = useQueryClient();
  const agent = dashboard.agent;
  const [lighterBinding, setLighterBinding] = useState<ExecutionBindingRecord | null>(null);
  const [robinhoodBinding, setRobinhoodBinding] = useState<ExecutionBindingRecord | null>(null);
  const [lighterAccountIndex, setLighterAccountIndex] = useState("");
  const [lighterNonce, setLighterNonce] = useState("0");
  const [lighterOwner, setLighterOwner] = useState<string>(auth.embeddedAddress ?? auth.accounts[0]?.address ?? "");
  useEffect(() => {
    if (!lighterOwner && auth.accounts.length) setLighterOwner(auth.accounts[0].address);
  }, [auth.accounts, lighterOwner]);
  const hasAccount = agent?.mode === "live" && agent.status !== "setup";
  const readiness = useQuery({
    queryKey: ["agent-readiness", agent?.id],
    queryFn: () => api.agentReadiness(agent!.id),
    enabled: hasAccount,
    retry: false,
  });
  const lighter = useMutation({
    mutationFn: () => {
      if (!agent || !lighterOwner) throw new Error("Link an execution wallet first.");
      const accountIndex = Number(lighterAccountIndex);
      const nonce = Number(lighterNonce);
      if (!Number.isSafeInteger(accountIndex) || accountIndex <= 0) throw new Error("Enter the new Lighter subaccount index.");
      if (!Number.isSafeInteger(nonce) || nonce < 0) throw new Error("Enter the current Lighter change nonce.");
      return api.requestLighterLink(agent.id, lighterOwner, accountIndex, nonce);
    },
    onSuccess: (binding) => {
      setLighterBinding(binding);
      void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
    },
  });
  const lighterConfirm = useMutation({
    mutationFn: async () => {
      if (!agent || !lighterBinding?.providerRequestId || !lighterBinding.associationPayload) {
        throw new Error("The Lighter association payload is not ready.");
      }
      const signature = await auth.signMessage(lighterBinding.associationPayload, lighterBinding.ownerAddress);
      return api.confirmLighterLink(agent.id, {
        requestId: lighterBinding.requestId,
        linkId: lighterBinding.providerRequestId,
        l1Signature: signature,
      });
    },
    onSuccess: (binding) => {
      setLighterBinding(binding);
      void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
      void queryClient.invalidateQueries({ queryKey: ["agent-readiness"] });
    },
  });
  const robinhood = useMutation({
    mutationFn: () => {
      if (!agent) throw new Error("Create the agent first.");
      return api.prepareRobinhood(agent.id);
    },
    onSuccess: (binding) => {
      setRobinhoodBinding(binding);
      void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
    },
  });
  const lifecycle = useMutation({
    mutationFn: (command: "close" | "withdraw") => {
      if (!agent) throw new Error("Create the agent first.");
      return api.agentCommand(agent.id, command);
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
      void queryClient.invalidateQueries({ queryKey: ["agent-readiness"] });
    },
  });

  if (!agent || agent.mode !== "live") return null;
  const state = readiness.data;
  const requirements = [
    { label: "Robinhood USDG", ready: Boolean(state?.robinhoodDeployed && state.robinhoodFunded), detail: "Vault deployed and funded on Robinhood Chain mainnet" },
    { label: "Lighter USDC", ready: Boolean(state?.lighterLinked && state.lighterFunded), detail: "User-owned subaccount linked and collateralized" },
    { label: "User ETH gas", ready: Boolean(state?.userGasReady), detail: "Pays deployment and owner transactions without sponsorship" },
    { label: "Execution ETH gas", ready: Boolean(state?.executionGasReady), detail: "Funds the isolated execution signer" },
    { label: "Policy and reconciliation", ready: Boolean(state?.policyActive && state.reconciled), detail: "Independent services must verify both accounts" },
  ];

  return (
    <section className="panel mainnet-readiness">
      <div className="panel-heading">
        <div><span className="eyebrow">Mainnet execution</span><h2>{agentStatusLabel(agent.status)}</h2></div>
        <span className={`status-pill ${state?.canLaunch ? "running" : "halted"}`}>{state?.canLaunch ? "Ready" : "Blocked"}</span>
      </div>
      <p className="readiness-copy">AAPL only, capped at $25 per leg. Each venue is funded separately. Alchemy sponsorship is optional; ETH is the fallback for every owner-paid transaction.</p>
      <div className="readiness-grid">
        {requirements.map((requirement) => (
          <article key={requirement.label}>
            <span className={`status-dot ${requirement.ready ? "running" : "halted"}`} />
            <div><strong>{requirement.label}</strong><small>{requirement.detail}</small></div>
          </article>
        ))}
      </div>
      {agent.status === "setup" ? (
        <small>Set up the execution account before linking venues or funding capital.</small>
      ) : (
        <>
          <div className="button-row">
            <label>Lighter owner<select value={lighterOwner} onChange={(event) => setLighterOwner(event.target.value)}>{auth.accounts.map((account) => <option key={account.address} value={account.address}>{account.label}</option>)}</select></label>
            <label>Lighter subaccount index<input inputMode="numeric" value={lighterAccountIndex} onChange={(event) => setLighterAccountIndex(event.target.value)} /></label>
            <label>Lighter change nonce<input inputMode="numeric" value={lighterNonce} onChange={(event) => setLighterNonce(event.target.value)} /></label>
          </div>
          <div className="button-row">
            <button className="button button-secondary" disabled={lighter.isPending} onClick={() => lighter.mutate()}>{lighter.isPending ? "Requesting…" : "Request Lighter provisioning"}</button>
            {lighterBinding?.status === "awaiting_signature" && <button className="button button-primary" disabled={lighterConfirm.isPending} onClick={() => lighterConfirm.mutate()}>{lighterConfirm.isPending ? "Verifying…" : "Sign Lighter association"}</button>}
            <button className="button button-secondary" disabled={robinhood.isPending} onClick={() => robinhood.mutate()}>{robinhood.isPending ? "Preparing…" : "Prepare Robinhood deployment"}</button>
            {!matchesTerminal(agent.status) && <button className="button button-quiet danger" disabled={lifecycle.isPending} onClick={() => lifecycle.mutate("close")}>Request close</button>}
            {agent.status === "closed" && <button className="button button-secondary" disabled={lifecycle.isPending || !state?.reconciled} onClick={() => lifecycle.mutate("withdraw")}>Prepare owner withdrawal</button>}
          </div>
        </>
      )}
      {lighterBinding && <small>Lighter request {lighterBinding.requestId}: {lighterBinding.status}. The user-owned L1 wallet must sign the association payload before verification can complete.</small>}
      {robinhoodBinding && <small>Robinhood request {robinhoodBinding.requestId}: {robinhoodBinding.status}. Deployment and deposit remain owner-controlled transactions.</small>}
      <small>The product API stores only public binding references. Wallet private keys and secret Lighter API keys are never accepted here. Commands stay pending until execution and reconciliation services return evidence. Withdrawals require an owner signature.</small>
      {(readiness.error || lighter.error || lighterConfirm.error || robinhood.error || lifecycle.error) && <ErrorNotice error={readiness.error ?? lighter.error ?? lighterConfirm.error ?? robinhood.error ?? lifecycle.error} />}
    </section>
  );
}

function matchesTerminal(status: AgentStatus) {
  return status === "setup" || status === "closing" || status === "closed";
}

export function MandateButton({ dashboard }: { dashboard: DashboardSnapshot }) {
  const smartWallet = useSmartWallet();
  const queryClient = useQueryClient();
  const vault = dashboard.vault;
  const mutation = useMutation({
    mutationFn: async () => {
      if (!vault) throw new Error("Strategy controls are not configured.");
      return smartWallet.executeCalls([mandateCall(vault.record.guardAddress, !vault.halted)]);
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
      if (!vault || !auth.embeddedAddress) throw new Error("Withdrawal is not available.");
      const raw = parseTokenAmount(amount, vault.balance.decimals);
      if (raw > BigInt(vault.balance.raw)) throw new Error("Amount exceeds the vault balance.");
      return smartWallet.executeCalls([
        withdrawalCall(vault.record.vaultAddress, auth.embeddedAddress, raw),
      ]);
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
      <small>Destination: {formatAddress(auth.embeddedAddress)}</small>
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
      if (!vault || !wallet) throw new Error("Choose a connected funding wallet.");
      const raw = parseTokenAmount(amount, vault.balance.decimals);
      return smartWallet.executeCalls(
        depositCalls(vault.record.assetAddress, vault.record.vaultAddress, raw),
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
      <small>Approval and deposit are submitted as one smart-account batch.</small>
      <button className="button button-primary" disabled={mutation.isPending || !amount || !wallet} type="submit">{mutation.isPending ? "Adding funds…" : "Add funds"}</button>
      {mutation.error && <ErrorNotice error={mutation.error} />}
    </form>
  );
}
