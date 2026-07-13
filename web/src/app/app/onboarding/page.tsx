"use client";

import Link from "next/link";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRouter } from "next/navigation";
import { useEffect, useRef, useState } from "react";
import { formatEther } from "viem";
import { ErrorNotice, LoadingPanel, PageHeader } from "../../../components/app-ui";
import { useAppApi, useRobinAuth, useSmartWallet } from "../../../components/app-providers";
import { robinhoodAppChainId } from "../../../lib/chain";
import { formatAddress } from "../../../lib/format";

const PENDING_KEY = "robin:onboarding-call-id";

export default function OnboardingPage() {
  const api = useAppApi();
  const auth = useRobinAuth();
  const smartWallet = useSmartWallet();
  const router = useRouter();
  const queryClient = useQueryClient();
  const resumed = useRef(false);
  const identityVersion = useRef("");
  const startedAt = useRef<number | undefined>(undefined);
  const [phase, setPhase] = useState<"idle" | "preparing" | "signing" | "confirming" | "delayed" | "complete">("idle");
  const [error, setError] = useState<unknown>();
  const query = useQuery({ queryKey: ["me"], queryFn: () => api.syncWallets() });
  const sync = useMutation({
    mutationFn: () => api.syncWallets(),
    onSuccess: (data) => queryClient.setQueryData(["me"], data),
  });
  useEffect(() => {
    const next = `${auth.hasRecovery}:${auth.accounts.length}`;
    if (!identityVersion.current) {
      identityVersion.current = next;
      return;
    }
    if (identityVersion.current === next) return;
    identityVersion.current = next;
    sync.mutate();
  }, [auth.accounts.length, auth.hasRecovery, sync]);

  useEffect(() => {
    if (resumed.current || !query.data || query.data.vault) return;
    const callId = window.localStorage.getItem(PENDING_KEY);
    if (!callId) return;
    resumed.current = true;
    setPhase("confirming");
    void api.confirmVault(callId).then(() => {
      window.localStorage.removeItem(PENDING_KEY);
      setPhase("complete");
      void queryClient.invalidateQueries();
    }).catch((resumeError) => {
      setPhase("delayed");
      setError(resumeError);
    });
  }, [api, query.data, queryClient]);

  const activate = async () => {
    setError(undefined);
    startedAt.current = Date.now();
    void api.metric("onboarding_started").catch(() => undefined);
    try {
      const gas = await smartWallet.refreshGasStatus();
      if (!gas.sponsored && gas.balance === 0n) {
        throw new Error(`Send ETH on Robinhood Chain to ${me.smartAccount?.address} before creating the vault.`);
      }
      setPhase("preparing");
      const plan = await api.prepareVault();
      if (plan.chainId !== robinhoodAppChainId) {
        throw new Error("The vault plan targets the wrong network.");
      }
      setPhase("signing");
      const callId = await smartWallet.executeCalls(plan.calls, undefined, (submittedId) => {
        window.localStorage.setItem(PENDING_KEY, submittedId);
        setPhase("confirming");
      });
      void api.metric("user_operation_included", Date.now() - (startedAt.current ?? Date.now()), "success").catch(() => undefined);
      setPhase("confirming");
      await api.confirmVault(callId);
      window.localStorage.removeItem(PENDING_KEY);
      setPhase("complete");
      void api.metric("onboarding_completed", Date.now() - (startedAt.current ?? Date.now()), "success").catch(() => undefined);
      await queryClient.invalidateQueries();
      router.replace("/app");
    } catch (activationError) {
      setError(activationError);
      if (window.localStorage.getItem(PENDING_KEY)) {
        void api.metric("onboarding_confirmation_delayed", undefined, "pending").catch(() => undefined);
      }
      setPhase(window.localStorage.getItem(PENDING_KEY) ? "delayed" : "idle");
    }
  };

  const retryConfirmation = async () => {
    const callId = window.localStorage.getItem(PENDING_KEY);
    if (!callId) return;
    setError(undefined);
    setPhase("confirming");
    try {
      await api.confirmVault(callId);
      window.localStorage.removeItem(PENDING_KEY);
      setPhase("complete");
      await queryClient.invalidateQueries();
      router.replace("/app");
    } catch (confirmationError) {
      setError(confirmationError);
      setPhase("delayed");
    }
  };

  const refreshGas = () => {
    setError(undefined);
    void smartWallet.refreshGasStatus().catch(setError);
  };

  if (query.isLoading) return <LoadingPanel label="Restoring setup…" />;
  if (query.error || !query.data) return <ErrorNotice error={query.error} retry={() => void query.refetch()} />;
  const me = query.data;
  const recoveryReady = me.user.hasRecovery || auth.hasRecovery;
  const gas = smartWallet.gasStatus;
  const needsGas = gas?.sponsored === false && gas.balance === 0n;

  if (me.vault || phase === "complete") {
    return (
      <section className="onboarding-complete">
        <span className="success-mark">✓</span><span className="eyebrow">Setup complete</span>
        <h1>Strategy vault active</h1>
        <p>Your vault is funded and ready for strategy control.</p>
        <Link className="button button-primary" href="/app">Open dashboard</Link>
      </section>
    );
  }

  return (
    <>
      <PageHeader eyebrow="Account setup" title="Create your strategy vault" description="Establish recovery, verify wallets, and deploy the vault in one signed operation." />
      <ol className="onboarding-steps">
        <Step number="1" title="Smart account" complete={Boolean(me.smartAccount)}>
          {me.smartAccount ? <p>Ready at <code>{formatAddress(me.smartAccount.address)}</code></p> : <p>Creating a durable smart account.</p>}
        </Step>
        <Step number="2" title="Recovery" complete={recoveryReady}>
          {recoveryReady ? <p>Email or passkey recovery is connected.</p> : <><p>Add a durable way to recover your account before funding the vault.</p><div className="button-row"><button className="button button-secondary" onClick={auth.linkEmail}>Add email</button><button className="button button-secondary" onClick={auth.linkPasskey}>Add passkey</button><button className="button button-quiet" disabled={sync.isPending} onClick={() => sync.mutate()}>I’ve added it</button></div></>}
        </Step>
        <Step number="3" title="Funding wallets" complete={me.wallets.length > 0} optional>
          <p>{me.wallets.length} wallet{me.wallets.length === 1 ? "" : "s"} linked. Connect a supported wallet now or after setup.</p>
          <div className="button-row"><button className="button button-secondary" onClick={auth.linkWallet}>Link wallet</button><button className="button button-quiet" disabled={sync.isPending} onClick={() => sync.mutate()}>Refresh</button></div>
        </Step>
        <Step number="4" title="Network fee" complete={Boolean(gas?.sponsored || (gas?.balance && gas.balance > 0n))}>
          {!gas ? (
            <><p>Checking the Robin account’s ETH balance.</p><div className="button-row"><button className="button button-quiet" onClick={refreshGas}>Check balance</button></div></>
          ) : gas.sponsored ? (
            <p>Network fees are sponsored.</p>
          ) : gas.balance && gas.balance > 0n ? (
            <><p>{formatGasBalance(gas.balance)} ETH is available for Robinhood Chain fees.</p><div className="button-row"><button className="button button-quiet" onClick={refreshGas}>Refresh</button></div></>
          ) : (
            <><p>Send ETH on Robinhood Chain to <code>{me.smartAccount?.address}</code>. The embedded account pays gas when sponsorship is disabled.</p><div className="button-row"><button className="button button-secondary" onClick={() => void navigator.clipboard.writeText(me.smartAccount?.address ?? "").catch(setError)}>Copy address</button><button className="button button-quiet" onClick={refreshGas}>Refresh balance</button></div></>
          )}
        </Step>
        <Step number="5" title="Personal vault" complete={false}>
          <p>One batch creates your testnet vault and funds its initial balance.</p>
        </Step>
      </ol>
      {phase === "delayed" ? (
        <div className="notice notice-warning" role="status">
          <div><strong>Confirmation pending</strong><p>The transaction was submitted successfully. Confirmation will resume without creating another vault.</p>{error instanceof Error && <small>{error.message}</small>}</div>
          <button className="button button-primary" onClick={() => void retryConfirmation()}>Check again</button>
        </div>
      ) : (
        <section className="activate-panel">
          <div><strong>{phaseLabel(phase)}</strong><p>One signature. {gas?.sponsored ? "Network fees are sponsored." : "Network fees are paid in ETH by your Robin account."}</p></div>
          <button className="button button-primary" disabled={!recoveryReady || !me.smartAccount || needsGas || phase !== "idle"} onClick={() => void activate()}>{phase === "idle" ? "Create vault" : "Working…"}</button>
        </section>
      )}
      {error && phase !== "delayed" && <ErrorNotice error={error} />}
    </>
  );
}

function formatGasBalance(balance: bigint) {
  const value = formatEther(balance);
  if (balance < 1_000_000_000_000n) return "less than 0.000001";
  return Number(value).toLocaleString(undefined, { maximumFractionDigits: 6 });
}

function Step({ number, title, complete, optional = false, children }: { number: string; title: string; complete: boolean; optional?: boolean; children: React.ReactNode }) {
  return <li className={complete ? "complete" : ""}><span className="step-number">{complete ? "✓" : number}</span><div><h2>{title}{optional && <small>Optional</small>}</h2>{children}</div></li>;
}

function phaseLabel(phase: string) {
  if (phase === "preparing") return "Preparing vault";
  if (phase === "signing") return "Awaiting signature";
  if (phase === "confirming") return "Confirming transaction";
  return "Ready to deploy";
}
