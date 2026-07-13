"use client";

import Link from "next/link";
import type { ActivityRecord } from "../lib/app-types";
import { mainnetTransactionUrl, robinhoodMainnetChainId } from "../lib/chain";
import { formatAddress, formatDate, titleFromKind } from "../lib/format";

export function PageHeader({ eyebrow, title, description, action }: {
  eyebrow: string;
  title: string;
  description: string;
  action?: React.ReactNode;
}) {
  return (
    <header className="page-header">
      <div><span className="eyebrow">{eyebrow}</span><h1>{title}</h1><p>{description}</p></div>
      {action && <div className="page-actions">{action}</div>}
    </header>
  );
}

export function EmptyState({ title, body, action }: { title: string; body: string; action?: React.ReactNode }) {
  return <div className="empty-state"><span>○</span><h3>{title}</h3><p>{body}</p>{action}</div>;
}

export function ErrorNotice({ error, retry }: { error: unknown; retry?: () => void }) {
  const message = error instanceof Error ? error.message : "Something went wrong.";
  return (
    <div className="notice notice-error" role="alert">
      <div><strong>Request failed</strong><p>{message}</p></div>
      {retry && <button className="button button-secondary" onClick={retry}>Try again</button>}
    </div>
  );
}

export function LoadingPanel({ label = "Loading account…" }: { label?: string }) {
  return <div className="loading-panel" role="status"><i />{label}</div>;
}

export function ActivityList({ items, compact = false }: { items: ActivityRecord[]; compact?: boolean }) {
  if (!items.length) return <EmptyState title="No activity" body="Account and strategy events will appear here." />;
  return (
    <div className={`activity-list ${compact ? "compact" : ""}`}>
      {items.map((item) => (
        <article key={item.id}>
          <span className="activity-icon" aria-hidden="true">↗</span>
          <div><strong>{titleFromKind(item.kind)}</strong><small>{formatDate(item.occurredAt)}</small></div>
          {item.transactionHash && item.chainId === robinhoodMainnetChainId && <a href={mainnetTransactionUrl(item.transactionHash)} target="_blank" rel="noreferrer" aria-label={`View ${titleFromKind(item.kind)} transaction`}>{formatAddress(item.transactionHash)}</a>}
        </article>
      ))}
    </div>
  );
}

export function SetupCard() {
  return (
    <section className="setup-card">
      <div><span className="eyebrow">Strategy vault</span><h2>Create your strategy vault</h2><p>Establish and fund your mainnet vault in one sponsored operation.</p></div>
      <Link className="button button-primary" href="/app/onboarding">Create vault</Link>
    </section>
  );
}
