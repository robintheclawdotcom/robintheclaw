"use client";

import { useQuery } from "@tanstack/react-query";
import { EmptyState, ErrorNotice, LoadingPanel, PageHeader, SetupCard } from "../../../components/app-ui";
import { useAppApi } from "../../../components/app-providers";
import { AddFundsForm, AgentButton, MainnetReadinessPanel, MandateButton, WithdrawForm } from "../../../components/strategy-controls";
import { agentStatusLabel } from "../../../lib/agent-lifecycle";
import { formatAddress, formatAmount } from "../../../lib/format";

export default function StrategyPage() {
  const api = useAppApi();
  const query = useQuery({ queryKey: ["dashboard"], queryFn: () => api.dashboard() });
  if (query.isLoading) return <LoadingPanel />;
  if (query.error || !query.data) return <ErrorNotice error={query.error} retry={() => void query.refetch()} />;
  const dashboard = query.data;
  const vault = dashboard.vault;
  const isLive = dashboard.agent?.mode === "live";

  return (
    <>
      <PageHeader eyebrow="Live strategy" title="AAPL execution" description="Connect both venues, verify funding, and operate the basis-aapl-v1 agent." action={<AgentButton dashboard={dashboard} />} />
      <MainnetReadinessPanel dashboard={dashboard} />
      {isLive ? (
        <section className="panel">
          <div className="panel-heading"><div><span className="eyebrow">Fixed live mandate</span><h2>AAPL spot + perpetual</h2></div></div>
          <p>The agent buys AAPL Stock Token on Robinhood Chain and sells the matching Lighter perpetual. Each account has its own owner-controlled vault, signer, credentials, and nonces.</p>
        </section>
      ) : !vault ? <SetupCard /> : (
        <>
          <div className="strategy-layout">
            <section className="panel">
              <div className="panel-heading"><div><span className="eyebrow">Robin agent</span><h2>{dashboard.agent ? `Agent ${agentStatusLabel(dashboard.agent.status)}` : "Not launched"}</h2></div>{dashboard.agent && <span className={`status-pill ${dashboard.agent.status === "running" ? "running" : "halted"}`}>{dashboard.agent.mode === "live" ? "Live" : "Paper"}</span>}</div>
              {dashboard.agent ? <dl className="detail-list large"><div><dt>Strategy</dt><dd>{dashboard.agent.strategyVersion}</dd></div><div><dt>Evaluations</dt><dd>{dashboard.agent.evaluations}</dd></div><div><dt>Candidates</dt><dd>{dashboard.agent.candidates}</dd></div><div><dt>Last evaluation</dt><dd>{dashboard.agent.lastEvaluatedAt ? new Date(dashboard.agent.lastEvaluatedAt).toLocaleString() : "Waiting"}</dd></div></dl> : <EmptyState title="No agent" body="Launch Robin to start the strategy runtime for this account." />}
            </section>
            <section className="panel">
              <div className="panel-heading"><div><span className="eyebrow">Vault mandate</span><h2>{vault.halted ? "Execution closed" : "Execution open"}</h2></div><MandateButton dashboard={dashboard} /></div>
              <dl className="detail-list large"><div><dt>Vault balance</dt><dd>{formatAmount(vault.balance)}</dd></div><div><dt>Remaining capacity</dt><dd>{formatAmount(vault.remainingCapacity)}</dd></div><div><dt>Current exposure</dt><dd>{formatAmount(dashboard.deployedCapital)}</dd></div><div><dt>Open positions</dt><dd>{dashboard.positions.filter((position) => position.status === "open").length}</dd></div></dl>
            </section>
            <section className="panel contract-card">
              <div className="panel-heading"><div><span className="eyebrow">Personal vault</span><h2>Version {vault.record.factoryVersion}</h2></div></div>
              <dl className="address-list"><div><dt>Owner</dt><dd>{formatAddress(dashboard.smartAccount?.address)}</dd></div><div><dt>Vault</dt><dd>{formatAddress(vault.record.vaultAddress)}</dd></div><div><dt>Guard</dt><dd>{formatAddress(vault.record.guardAddress)}</dd></div><div><dt>Attestation anchor</dt><dd>{formatAddress(vault.record.anchorAddress)}</dd></div></dl>
            </section>
          </div>
          <div className="strategy-layout action-layout">
            <section className="panel"><div className="panel-heading"><div><span className="eyebrow">Capital</span><h2>Add funds</h2></div></div><AddFundsForm dashboard={dashboard} /></section>
            <section className="panel"><div className="panel-heading"><div><span className="eyebrow">Capital</span><h2>Withdraw</h2></div></div><WithdrawForm dashboard={dashboard} /></section>
          </div>
          <section className="panel">
            <div className="panel-heading"><div><span className="eyebrow">Execution</span><h2>Positions</h2></div></div>
            {dashboard.positions.length ? <div className="position-cards">{dashboard.positions.map((position) => <article key={position.id}><strong>{position.symbol}</strong><span>{position.status}</span><dl><div><dt>Entry basis</dt><dd>{position.entryBasisBps} bps</dd></div><div><dt>Current basis</dt><dd>{position.currentBasisBps} bps</dd></div><div><dt>Funding</dt><dd>{formatAmount(position.funding)}</dd></div><div><dt>P&amp;L</dt><dd>{formatAmount(position.pnl)}</dd></div></dl></article>)}</div> : <EmptyState title="No positions" body="No executed positions are currently recorded." />}
          </section>
        </>
      )}
    </>
  );
}
