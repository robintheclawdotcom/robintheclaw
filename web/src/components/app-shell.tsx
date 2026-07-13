"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useState } from "react";
import { useRobinAuth } from "./app-providers";
import { formatAddress } from "../lib/format";

const navigation = [
  ["Overview", "/app"],
  ["Strategy", "/app/strategy"],
  ["Activity", "/app/activity"],
  ["Wallets", "/app/wallets"],
  ["Settings", "/app/settings"],
] as const;

export function AppShell({ children }: { children: React.ReactNode }) {
  const auth = useRobinAuth();
  const pathname = usePathname();
  const [menuOpen, setMenuOpen] = useState(false);

  if (!auth.configured) {
    return (
      <main className="app-auth">
        <AuthPanel
          eyebrow="Application unavailable"
          title="Provider configuration is incomplete."
          body="Contact the system administrator."
        />
      </main>
    );
  }

  if (!auth.ready) {
    return <main className="app-auth"><div className="app-loader" role="status">Restoring session…</div></main>;
  }

  if (!auth.authenticated) {
    return (
      <main className="app-auth">
        <AuthPanel
          eyebrow="Strategy operations"
          title="Run Robin from one account."
          body="Sign in to monitor the strategy, manage linked wallets, and activate your personal vault."
          action={<button className="button button-primary" onClick={auth.login}>Sign in</button>}
        />
      </main>
    );
  }

  return (
    <div className="app-root">
      <a className="skip-link" href="#app-content">Skip to content</a>
      <aside className={`app-sidebar ${menuOpen ? "open" : ""}`} aria-label="Application navigation">
        <div className="app-brand">
          <Link href="/" aria-label="Robin the Claw home">
            <img src="/brand/icon-48.png" alt="" aria-hidden="true" />
            <span>Robin</span>
          </Link>
          <button className="app-nav-close" onClick={() => setMenuOpen(false)} aria-label="Close navigation">×</button>
        </div>
        <nav>
          {navigation.map(([label, href]) => {
            const active = href === "/app" ? pathname === href : pathname.startsWith(href);
            return <Link key={href} href={href} className={active ? "active" : ""} onClick={() => setMenuOpen(false)}>{label}</Link>;
          })}
        </nav>
        <div className="app-sidebar-footer">
          <span className="network-status"><i /> Robinhood testnet</span>
          <Link href="/">Public site ↗</Link>
        </div>
      </aside>
      {menuOpen && <button className="app-nav-backdrop" onClick={() => setMenuOpen(false)} aria-label="Close navigation" />}
      <div className="app-workspace">
        <header className="app-topbar">
          <button className="app-nav-open" onClick={() => setMenuOpen(true)} aria-label="Open navigation">☰</button>
          <div>
            <span className="topbar-label">Strategy account</span>
            <strong>{formatAddress(auth.embeddedAddress)}</strong>
          </div>
          <button className="account-button" onClick={() => void auth.logout()}>Sign out</button>
        </header>
        <main className="app-main" id="app-content">{children}</main>
      </div>
    </div>
  );
}

function AuthPanel({
  eyebrow,
  title,
  body,
  action,
}: {
  eyebrow: string;
  title: string;
  body: string;
  action?: React.ReactNode;
}) {
  return (
    <section className="auth-panel">
      <Link className="auth-brand" href="/"><img src="/brand/icon-48.png" alt="" /> Robin the Claw</Link>
      <span className="eyebrow">{eyebrow}</span>
      <h1>{title}</h1>
      <p>{body}</p>
      {action}
      <div className="auth-benefits" aria-label="Account benefits">
        <span>Email or wallet sign-in</span><span>Gasless activation</span><span>Self-custodied vault</span>
      </div>
    </section>
  );
}
