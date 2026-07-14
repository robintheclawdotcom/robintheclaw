"use client";

import Link from "next/link";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRouter } from "next/navigation";
import { useEffect, useRef, useState } from "react";
import { formatEther } from "viem";
import { ErrorNotice, LoadingPanel, PageHeader } from "../../../components/app-ui";
import { useAppApi, useRobinAuth, useSmartWallet } from "../../../components/app-providers";
import { formatAddress } from "../../../lib/format";

export default function OnboardingPage() {
  const api = useAppApi();
  const auth = useRobinAuth();
  const smartWallet = useSmartWallet();
  const router = useRouter();
  const queryClient = useQueryClient();
  const identityVersion = useRef("");
  const [error, setError] = useState<unknown>();
  const meQuery = useQuery({ queryKey: ["me"], queryFn: () => api.syncWallets() });
  const dashboardQuery = useQuery({ queryKey: ["dashboard"], queryFn: () => api.dashboard() });
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

  const start = useMutation({
    mutationFn: async () => {
      const current = dashboardQuery.data?.agent;
      const agent = current ?? await api.launchAgent();
      if (agent.status === "setup") await api.createExecutionAccount(agent.id);
    },
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["dashboard"] }),
        queryClient.invalidateQueries({ queryKey: ["agent-readiness"] }),
      ]);
      router.push("/app/strategy");
    },
  });

  const refreshGas = () => {
    setError(undefined);
    void smartWallet.refreshGasStatus().catch(setError);
  };

  if (meQuery.isLoading || dashboardQuery.isLoading) return <LoadingPanel label="Preparing live setup…" />;
  if (meQuery.error || !meQuery.data) return <ErrorNotice error={meQuery.error} retry={() => void meQuery.refetch()} />;
  if (dashboardQuery.error || !dashboardQuery.data) return <ErrorNotice error={dashboardQuery.error} retry={() => void dashboardQuery.refetch()} />;
  const me = meQuery.data;
  const agent = dashboardQuery.data.agent;
  const recoveryReady = me.user.hasRecovery || auth.hasRecovery;
  const gas = smartWallet.gasStatus;
  const gasReady = Boolean(gas?.balance && gas.balance > 0n);

  return (
    <>
      <PageHeader
        eyebrow="Live setup"
        title="Launch your AAPL agent"
        description="Verify the owner account, create an isolated execution account, then connect and fund both venues."
      />
      <ol className="onboarding-steps">
        <Step number="1" title="Owner account" complete={Boolean(me.smartAccount)}>
          {me.smartAccount
            ? <p>Robinhood Chain owner ready at <code>{formatAddress(me.smartAccount.address)}</code>.</p>
            : <p>Creating the account that owns the Robinhood vault and authorizes withdrawals.</p>}
        </Step>
        <Step number="2" title="Recovery" complete={recoveryReady}>
          {recoveryReady ? <p>Email or passkey recovery is connected.</p> : <>
            <p>Add a durable recovery method before creating an execution account.</p>
            <div className="button-row">
              <button className="button button-secondary" onClick={auth.linkEmail}>Add email</button>
              <button className="button button-secondary" onClick={auth.linkPasskey}>Add passkey</button>
              <button className="button button-quiet" disabled={sync.isPending} onClick={() => sync.mutate()}>Refresh recovery</button>
            </div>
          </>}
        </Step>
        <Step number="3" title="Signing wallets" complete={me.wallets.length > 0}>
          <p>{me.wallets.length} wallet{me.wallets.length === 1 ? "" : "s"} linked. The selected owner signs the Lighter association and Robinhood deployment.</p>
          <div className="button-row">
            <button className="button button-secondary" onClick={auth.linkWallet}>Link wallet</button>
            <button className="button button-quiet" disabled={sync.isPending} onClick={() => sync.mutate()}>Sync wallets</button>
          </div>
        </Step>
        <Step number="4" title="Owner ETH" complete={gasReady}>
          {!gas ? <>
            <p>Check the owner account for Robinhood Chain ETH.</p>
            <button className="button button-quiet" onClick={refreshGas}>Check ETH balance</button>
          </> : gas.balance && gas.balance > 0n ? <>
            <p>{formatGasBalance(gas.balance)} ETH is available for deployment and owner transactions.</p>
            <button className="button button-quiet" onClick={refreshGas}>Refresh ETH</button>
          </> : <>
            <p>Send ETH on Robinhood Chain to <code>{me.smartAccount?.address}</code> for deployment and owner transactions.</p>
            <div className="button-row">
              <button className="button button-secondary" onClick={() => void navigator.clipboard.writeText(me.smartAccount?.address ?? "").catch(setError)}>Copy owner address</button>
              <button className="button button-quiet" onClick={refreshGas}>Refresh ETH</button>
            </div>
          </>}
        </Step>
        <Step number="5" title="Live execution account" complete={Boolean(agent && agent.status !== "setup")}>
          {agent
            ? <p><code>{agent.strategyVersion}</code> is {agent.status.replaceAll("_", " ")}.</p>
            : <p>Create the fixed <code>basis-aapl-v1</code> agent and its isolated execution account.</p>}
        </Step>
      </ol>
      <section className="activate-panel">
        <div>
          <strong>{agent ? "Continue live setup" : "Create live agent"}</strong>
          <p>Next: link a user-owned Lighter subaccount, deploy the Robinhood graph, fund USDG and USDC, then launch.</p>
        </div>
        <button
          className="button button-primary"
          disabled={!recoveryReady || !me.smartAccount || start.isPending}
          onClick={() => start.mutate()}
        >
          {start.isPending ? "Creating execution account…" : agent && agent.status !== "setup" ? "Open live setup" : "Create execution account"}
        </button>
      </section>
      {!gasReady && me.smartAccount && <p className="page-footnote">You can create the account now and fund owner ETH before the Robinhood transaction.</p>}
      {(error || start.error || sync.error) && <ErrorNotice error={error ?? start.error ?? sync.error} />}
      {agent?.status === "closed" && <Link className="button button-secondary" href="/app/strategy">Open closed agent</Link>}
    </>
  );
}

function formatGasBalance(balance: bigint) {
  const value = formatEther(balance);
  if (balance < 1_000_000_000_000n) return "less than 0.000001";
  return Number(value).toLocaleString(undefined, { maximumFractionDigits: 6 });
}

function Step({ number, title, complete, children }: { number: string; title: string; complete: boolean; children: React.ReactNode }) {
  return <li className={complete ? "complete" : ""}><span className="step-number">{complete ? "✓" : number}</span><div><h2>{title}</h2>{children}</div></li>;
}
