"use client";

import Link from "next/link";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { ActivityList, EmptyState, ErrorNotice, LoadingPanel, PageHeader, SetupCard } from "../../components/app-ui";
import { useAppApi } from "../../components/app-providers";
import { MandateButton } from "../../components/strategy-controls";
import { formatAddress, formatAmount, formatDate } from "../../lib/format";

export default function OverviewPage() {
  const api = useAppApi();
  const startedAt = useRef(Date.now());
  const measured = useRef(false);
  const query = useQuery({ queryKey: ["dashboard"], queryFn: () => api.dashboard() });

  useEffect(() => {
    if (!query.data || measured.current) return;
    measured.current = true;
    void api.metric("dashboard_load", Date.now() - startedAt.current, "success").catch(() => undefined);
  }, [api, query.data]);

  if (query.isLoading) return <LoadingPanel label="Loading your strategy account…" />;
  if (query.error || !query.data) return <ErrorNotice error={query.error} retry={() => void query.refetch()} />;
  const dashboard = query.data;

  return (
    <>
      <PageHeader
        eyebrow="Overview"
        title="Your strategy account"
        description={`Capital, strategy, and performance as of ${formatDate(dashboard.asOf)}.`}
        action={dashboard.vault ? <MandateButton dashboard={dashboard} /> : <Link className="button button-primary" href="/app/onboarding">Set up Robin</Link>}
      />
      {!dashboard.vault && <SetupCard />}
      {dashboard.vault && <nav className="quick-actions" aria-label="Primary account actions"><Link className="button button-secondary" href="/app/strategy#fund">Add funds</Link><Link className="button button-secondary" href="/app/strategy#withdraw">Withdraw</Link><Link className="button button-secondary" href="/app/wallets">Link wallet</Link></nav>}
      <section className="metric-grid" aria-label="Account balances">
        <Metric label="Total account value" value={formatAmount(dashboard.totalValue)} />
        <Metric label="Available balance" value={formatAmount(dashboard.availableBalance)} />
        <Metric label="Deployed capital" value={formatAmount(dashboard.deployedCapital)} />
        <Metric label="Actual P&L" value={formatAmount(dashboard.pnl)} muted={!dashboard.pnl} />
      </section>
      <div className="dashboard-grid">
        <section className="panel strategy-summary">
          <div className="panel-heading"><div><span className="eyebrow">Strategy</span><h2>Basis strategy</h2></div><Link href="/app/strategy">Manage →</Link></div>
          {dashboard.vault ? (
            <>
              <div className="strategy-state"><span className={`status-dot ${dashboard.vault.halted ? "halted" : "running"}`} /><div><strong>{dashboard.vault.halted ? "Paused" : "Running"}</strong><small>{dashboard.vault.halted ? "Capital stays under your control." : "Watching for qualified opportunities."}</small></div></div>
              <dl className="detail-list"><div><dt>Current exposure</dt><dd>{formatAmount(dashboard.deployedCapital)}</dd></div><div><dt>Mandate capacity</dt><dd>{formatAmount(dashboard.vault.remainingCapacity)}</dd></div><div><dt>Open positions</dt><dd>{dashboard.positions.filter((position) => position.status === "open").length}</dd></div></dl>
            </>
          ) : <EmptyState title="No strategy vault yet" body="Complete the one-click setup to activate your personal vault." />}
        </section>
        <section className="panel opportunity-panel">
          <div className="panel-heading"><div><span className="eyebrow">Market intelligence</span><h2>Current opportunities</h2></div></div>
          {dashboard.opportunities.length ? (
            <div className="opportunity-list">{dashboard.opportunities.slice(0, 5).map((opportunity) => <article key={`${opportunity.symbol}-${opportunity.observedAt}`}><div><strong>{opportunity.symbol}</strong><small>{formatDate(opportunity.observedAt)}</small></div><span>{opportunity.basisBps} bps</span></article>)}</div>
          ) : <EmptyState title="Scanning the market" body="Qualified basis opportunities will appear when the research service observes them." />}
        </section>
      </div>
      <section className="panel positions-panel">
        <div className="panel-heading"><div><span className="eyebrow">Positions</span><h2>Open and closed positions</h2></div></div>
        {dashboard.positions.length ? <div className="data-table" role="region" aria-label="Positions" tabIndex={0}><table><thead><tr><th>Market</th><th>Status</th><th>Spot leg</th><th>Perp leg</th><th>Entry basis</th><th>Current basis</th><th>Funding</th><th>P&amp;L</th></tr></thead><tbody>{dashboard.positions.map((position) => <tr key={position.id}><td>{position.symbol}</td><td>{position.status}</td><td>{formatAmount(position.spotLeg)}</td><td>{formatAmount(position.perpLeg)}</td><td>{position.entryBasisBps} bps</td><td>{position.currentBasisBps} bps</td><td>{formatAmount(position.funding)}</td><td>{formatAmount(position.pnl)}</td></tr>)}</tbody></table></div> : <EmptyState title="No positions yet" body="Robin will show real venue positions and actual P&L here after execution begins. Nothing is simulated." />}
      </section>
      <section className="panel">
        <div className="panel-heading"><div><span className="eyebrow">Account</span><h2>Recent activity</h2></div><Link href="/app/activity">View all →</Link></div>
        <ActivityList items={dashboard.activity} compact />
      </section>
      <section className="panel">
        <div className="panel-heading"><div><span className="eyebrow">Unified capital</span><h2>Linked wallet balances</h2></div><Link href="/app/wallets">Manage wallets →</Link></div>
        <div className="linked-balance-list">{dashboard.wallets.map(({ wallet, balance }) => <article key={wallet.id}><div><strong>{wallet.label ?? wallet.walletType}</strong><small>{formatAddress(wallet.address)}</small></div><span>{formatAmount(balance)}</span></article>)}</div>
      </section>
    </>
  );
}

function Metric({ label, value, muted = false }: { label: string; value: string; muted?: boolean }) {
  return <article className="metric"><span>{label}</span><strong className={muted ? "muted" : ""}>{value}</strong>{muted && <small>Available when live positions exist</small>}</article>;
}
