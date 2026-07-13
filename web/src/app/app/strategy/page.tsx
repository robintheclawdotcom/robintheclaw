"use client";

import { useQuery } from "@tanstack/react-query";
import { EmptyState, ErrorNotice, LoadingPanel, PageHeader, SetupCard } from "../../../components/app-ui";
import { useAppApi } from "../../../components/app-providers";
import { AddFundsForm, MandateButton, WithdrawForm } from "../../../components/strategy-controls";
import { formatAddress, formatAmount } from "../../../lib/format";

export default function StrategyPage() {
  const api = useAppApi();
  const query = useQuery({ queryKey: ["dashboard"], queryFn: () => api.dashboard() });
  if (query.isLoading) return <LoadingPanel />;
  if (query.error || !query.data) return <ErrorNotice error={query.error} retry={() => void query.refetch()} />;
  const dashboard = query.data;
  const vault = dashboard.vault;

  return (
    <>
      <PageHeader eyebrow="Strategy" title="Control your basis strategy" description="Set the operating state, manage capital, and follow real positions as execution comes online." action={vault && <MandateButton dashboard={dashboard} />} />
      {!vault ? <SetupCard /> : (
        <>
          <div className="strategy-layout">
            <section className="panel">
              <div className="panel-heading"><div><span className="eyebrow">Mandate</span><h2>{vault.halted ? "Strategy paused" : "Strategy running"}</h2></div><span className={`status-pill ${vault.halted ? "halted" : "running"}`}>{vault.halted ? "Paused" : "Active"}</span></div>
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
            {dashboard.positions.length ? <div className="position-cards">{dashboard.positions.map((position) => <article key={position.id}><strong>{position.symbol}</strong><span>{position.status}</span><dl><div><dt>Entry basis</dt><dd>{position.entryBasisBps} bps</dd></div><div><dt>Current basis</dt><dd>{position.currentBasisBps} bps</dd></div><div><dt>Funding</dt><dd>{formatAmount(position.funding)}</dd></div><div><dt>P&amp;L</dt><dd>{formatAmount(position.pnl)}</dd></div></dl></article>)}</div> : <EmptyState title="No venue positions" body="The current release shows your mainnet vault and opportunity feed. Venue positions will appear only after they execute." />}
          </section>
        </>
      )}
    </>
  );
}
