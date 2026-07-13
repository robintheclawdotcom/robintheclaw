"use client";

import { useEffect, useRef, useState } from "react";
import { testnetProof } from "../lib/testnet-proof";

type DocId = "overview" | "signal" | "contracts" | "verifier" | "engine" | "testnet" | "methodology" | "readiness" | "architecture" | "developer" | "operations" | "security" | "venues";

const docs: Record<DocId, { file: string; title: string; body: React.ReactNode }> = {
  overview: {
    file: "README.md",
    title: "Overview",
    body: (
      <>
        <p>
          Robin the Claw is building the delta-neutral trading stack for tokenized markets on
          Robinhood Chain: market intelligence, adaptive sizing, matched execution, and a
          durable operating layer for autonomous strategies.
        </p>
        <h2>From market structure to a trade plan</h2>
        <p>
          It measures the basis between tokenized-equity spot liquidity and matching perpetuals,
          then turns a qualified opportunity into coordinated spot and perp legs.
        </p>
        <div className="note">
          On-chain records make each strategy run easier to inspect and improve over time.
        </div>
      </>
    ),
  },
  signal: {
    file: "signal/README.md",
    title: "Market intelligence",
    body: (
      <>
        <p>
          The scanner discovers Uniswap v4 stock-token pools, compares their on-chain spot prices
          with active Lighter perp marks, and builds the market data layer for strategy research.
        </p>
        <h2>Find the real opportunity</h2>
        <p>
          Robin ranks opportunities by freshness, liquidity, and spread quality, separating useful
          cross-venue dislocations from noisy market data.
        </p>
        <div className="code-block"><span>$</span> node signal/src/spot.mjs</div>
      </>
    ),
  },
  contracts: {
    file: "contracts/README.md",
    title: "Execution foundation",
    body: (
      <>
        <p>
          The on-chain foundation combines custody, delegated execution, venue policy, and
          operator control into a focused base for the Robin execution layer.
        </p>
        <h2>Built for expansion</h2>
        <p>
          The current testnet deployment establishes the contract relationships and operating
          controls that future venue adapters and position workflows will build on.
        </p>
        <div className="code-block"><span>$</span> cd contracts && forge test -vv</div>
      </>
    ),
  },
  verifier: {
    file: "verifier/README.md",
    title: "Record integrity",
    body: (
      <>
        <p>
          Robin can encode trade records deterministically, commit them on chain, and keep a
          durable history alongside strategy development.
        </p>
        <h2>A useful feedback loop</h2>
        <p>
          Record integrity supports research, operations, and public inspection without defining
          the product narrative.
        </p>
        <div className="code-block"><span>$</span> cd verifier && npm test</div>
      </>
    ),
  },
  testnet: {
    file: "deployments/testnet-proof.json",
    title: "Testnet foundation",
    body: (
      <>
        <p>
          The deployed testnet stack connects custody, strategy roles, and on-chain records on
          Robinhood Chain. It is the base for bringing venue integrations online in stages.
        </p>
        <h2>Connected on chain</h2>
        <p>
          The first testnet batch confirms the contract and record pipeline end to end. The
          accompanying verifier lets developers inspect that foundation directly.
        </p>
        <div className="code-block"><span>$</span> cd verifier && npm run verify:testnet-proof</div>
        <p>
          <a href={`${testnetProof.explorer}/tx/${testnetProof.transaction}`} target="_blank" rel="noreferrer">
            View the testnet anchor transaction ↗
          </a>
        </p>
        <PublishedDoc file="testnet-proof.md" />
      </>
    ),
  },
  engine: {
    file: "engine/README.md",
    title: "Trade planning engine",
    body: (
      <>
        <p>
          The Rust engine turns a basis observation into a matched spot and perp plan, combining
          market quality, fractional-Kelly sizing, exposure awareness, and delta neutrality.
        </p>
        <h2>Designed for repeatability</h2>
        <p>
          The engine gives the execution layer a clear plan to act on, so strategy development,
          operations, and venue integrations can evolve independently.
        </p>
        <div className="code-block"><span>$</span> cd engine && cargo test</div>
      </>
    ),
  },
  methodology: {
    file: "docs/research-methodology.md",
    title: "Edge research methodology",
    body: <PublishedDoc file="research-methodology.md" />,
  },
  readiness: {
    file: "docs/production-audit-mainnet-readiness.md",
    title: "Mainnet readiness",
    body: <PublishedDoc file="production-audit-mainnet-readiness.md" />,
  },
  architecture: { file: "docs/architecture.md", title: "Architecture", body: <PublishedDoc file="architecture.md" /> },
  developer: { file: "docs/developer-guide.md", title: "Developer guide", body: <PublishedDoc file="developer-guide.md" /> },
  operations: { file: "docs/operations.md", title: "Operations", body: <PublishedDoc file="operations.md" /> },
  security: { file: "docs/security-model.md", title: "Security model", body: <PublishedDoc file="security-model.md" /> },
  venues: { file: "docs/venue-gates.md", title: "Venue gates", body: <PublishedDoc file="venue-gates.md" /> },
};

function PublishedDoc({ file }: { file: string }) {
  const [content, setContent] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    fetch(`/docs/${file}`, { signal: controller.signal })
      .then((response) => {
        if (!response.ok) throw new Error(`documentation request failed (${response.status})`);
        return response.text();
      })
      .then(setContent)
      .catch((error: unknown) => {
        if (error instanceof DOMException && error.name === "AbortError") return;
        setContent(error instanceof Error ? error.message : "documentation request failed");
      });
    return () => controller.abort();
  }, [file]);

  return <pre className="published-doc">{content ?? "loading documentation…"}</pre>;
}

function Prompt({ children }: { children: React.ReactNode }) {
  return (
    <div className="prompt">
      <span>robin@claw</span><i>:</i><b>~/public</b><i>%</i>{children}
    </div>
  );
}

function DocsTree({ doc, onSelect }: { doc: DocId; onSelect: (id: DocId) => void }) {
  const entries = Object.keys(docs) as DocId[];

  return (
    <>
      <div className="tree-root">robin-the-claw/</div>
      {entries.map((id, index) => (
        <button className={doc === id ? "selected" : ""} key={id} onClick={() => onSelect(id)}>
          <span>{index === entries.length - 1 ? "└─" : "├─"}</span> {docs[id].file}
        </button>
      ))}
    </>
  );
}

export default function Home() {
  const [theme, setTheme] = useState<"dark" | "light">("dark");
  const [view, setView] = useState<"home" | "docs">("home");
  const [doc, setDoc] = useState<DocId>("overview");
  const [copied, setCopied] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const closeMenuRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    const saved = window.localStorage.getItem("rtc-theme");
    if (saved === "light" || saved === "dark") setTheme(saved);
  }, []);

  useEffect(() => {
    if (!menuOpen) return;

    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setMenuOpen(false);
    };

    document.body.style.overflow = "hidden";
    document.addEventListener("keydown", closeOnEscape);
    closeMenuRef.current?.focus();

    return () => {
      document.body.style.overflow = "";
      document.removeEventListener("keydown", closeOnEscape);
    };
  }, [menuOpen]);

  const toggleTheme = () => {
    const next = theme === "dark" ? "light" : "dark";
    setTheme(next);
    window.localStorage.setItem("rtc-theme", next);
  };

  const copyClone = async () => {
    try {
      await navigator.clipboard.writeText("git clone https://github.com/robintheclawdotcom/robintheclaw.git");
    } catch {
      return;
    }
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1600);
  };

  const openDocs = () => {
    setView("docs");
    setMenuOpen(false);
  };

  const openHome = () => {
    setView("home");
    setMenuOpen(false);
  };

  const selectDoc = (id: DocId) => {
    setDoc(id);
    setView("docs");
    setMenuOpen(false);
  };

  return (
    <main data-theme={theme}>
      <section className="terminal">
        <header className="titlebar">
          <div className="window-controls" aria-hidden="true"><span /><span /><span /></div>
          <nav className="desktop-nav" aria-label="Primary navigation">
            <button className={view === "home" ? "nav-active" : ""} onClick={openHome}>home</button>
            <button className={view === "docs" ? "nav-active" : ""} onClick={openDocs}>docs</button>
          </nav>
          <div className="terminal-title">robin@claw · /public · zsh</div>
          <div className="desktop-actions">
            <button className="theme-toggle" onClick={toggleTheme} aria-label="Toggle color theme">
              <span className="theme-dot" />{theme}
            </button>
            <a className="icon-link" href="https://github.com/robintheclawdotcom/robintheclaw" target="_blank" rel="noreferrer" aria-label="GitHub">
              <img className="github-mark" src="/icons/github-mark.svg" alt="" aria-hidden="true" />
            </a>
            <a className="icon-link" href="https://x.com/RobinTheClaw" target="_blank" rel="noreferrer" aria-label="X">𝕏</a>
          </div>
          <button
            className="menu-trigger"
            aria-label="Open navigation menu"
            aria-expanded={menuOpen}
            aria-controls="mobile-navigation"
            onClick={() => setMenuOpen(true)}
          >
            <span /><span /><span />
          </button>
        </header>

        {menuOpen && (
          <div className="mobile-navigation" id="mobile-navigation">
            <button className="menu-backdrop" aria-label="Close navigation menu" onClick={() => setMenuOpen(false)} />
            <aside className="menu-drawer" aria-label="Mobile navigation" role="dialog" aria-modal="true">
              <div className="drawer-header">
                <span>navigation</span>
                <button ref={closeMenuRef} onClick={() => setMenuOpen(false)} aria-label="Close navigation menu">×</button>
              </div>
              <div className="drawer-links">
                <button className={view === "home" ? "active" : ""} onClick={openHome}>home</button>
                <button className={view === "docs" ? "active" : ""} onClick={openDocs}>docs</button>
                <button className="drawer-theme" onClick={toggleTheme}><span className="theme-dot" />theme: {theme}</button>
              </div>
              {view === "docs" && <div className="drawer-docs"><DocsTree doc={doc} onSelect={selectDoc} /></div>}
              <div className="drawer-socials">
                <a href="https://github.com/robintheclawdotcom/robintheclaw" target="_blank" rel="noreferrer">github ↗</a>
                <a href="https://x.com/RobinTheClaw" target="_blank" rel="noreferrer">x / twitter ↗</a>
              </div>
            </aside>
          </div>
        )}

        <div className="content">
          {view === "home" ? (
            <div className="home-view">
              <section className="welcome">
                <img src="/brand/logo.jpg" alt="Robin the Claw pixel logo" />
                <div>
                  <h1><span>✻</span> Robin the Claw</h1>
                  <p>Delta-neutral market intelligence and execution for tokenized markets.</p>
                  <small>status: market intelligence → testnet foundation → execution stack</small>
                </div>
              </section>

              <section className="intro">
                <Prompt>robin init</Prompt>
                <p>
                  Robin the Claw is an autonomous trading stack for tokenized markets.
                  It finds cross-venue basis, evaluates the opportunity, sizes a position, and
                  prepares matched spot and perp legs for disciplined execution.
                </p>
              </section>

              <section className="clone">
                <Prompt>git clone</Prompt>
                <div className="clone-box">
                  <span>$</span>
                  <code>git clone https://github.com/robintheclawdotcom/robintheclaw.git</code>
                  <button onClick={copyClone}>{copied ? "copied ✓" : "copy"}</button>
                </div>
                <small>Open source foundations for market intelligence, trade planning, and execution.</small>
              </section>

              <section className="proof-status">
                <Prompt>robin testnet --status</Prompt>
                <div className="proof-panel">
                  <div>
                    <span className="proof-label">testnet foundation</span>
                    <strong>on-chain stack connected · sequence {testnetProof.sequence}</strong>
                    <p>Custody, strategy roles, and the record pipeline are live on Robinhood Chain testnet.</p>
                  </div>
                  <a href={`${testnetProof.explorer}/tx/${testnetProof.transaction}`} target="_blank" rel="noreferrer">
                    inspect on chain ↗
                  </a>
                </div>
              </section>

              <section>
                <Prompt>robin --components</Prompt>
                <div className="cards">
                  <article><span>[ signal ]</span><h2>Market intelligence</h2><p>Discovers v4 pools, compares spot with live perps, and maps the basis across tokenized markets.</p></article>
                  <article><span>[ engine ]</span><h2>Trade planning</h2><p>Combines spread quality, sizing, exposure awareness, and neutrality into a coordinated trade plan.</p></article>
                  <article><span>[ research ]</span><h2>Strategy research</h2><p>Builds the data, models, and market understanding that make each iteration stronger.</p></article>
                  <article><span>[ contracts ]</span><h2>Execution foundation</h2><p>Custody, delegated execution, and venue policy establish the base for the Robin execution layer.</p></article>
                  <article><span>[ verifier ]</span><h2>Record integrity</h2><p>On-chain records support inspection, research, and a durable history of strategy operations.</p></article>
                </div>
              </section>

              <section>
                <Prompt>robin explain --pipeline</Prompt>
                <div className="pipeline">
                  {[
                    ["01 · scan", "Map cross-venue market structure"],
                    ["02 · plan", "Size matched spot and perp legs"],
                    ["03 · execute", "Coordinate strategy and venue workflow"],
                    ["04 · learn", "Record each run and refine the system"],
                  ].map(([label, detail], index) => (
                    <div className="pipeline-item" key={label}>
                      <span>{label}</span><p>{detail}</p>{index < 3 && <b className="arrow">→</b>}
                    </div>
                  ))}
                </div>
              </section>

              <section className="docs-cta">
                <div><h2>Explore the execution stack</h2><p>Market intelligence, strategy planning, contracts, operations, and record integrity.</p></div>
                <button onClick={openDocs}>docs →</button>
              </section>
            </div>
          ) : (
            <div className="docs-view">
              <aside><DocsTree doc={doc} onSelect={selectDoc} /></aside>
              <article className="doc-article">
                <small>{docs[doc].file}</small>
                <h1>{docs[doc].title}</h1>
                {docs[doc].body}
              </article>
            </div>
          )}
        </div>

        <footer>
          <span>© 2026 Robin the Claw · built on Robinhood Chain</span>
          <span><a href="https://github.com/robintheclawdotcom/robintheclaw">github</a><a href="https://x.com/RobinTheClaw">x / twitter</a></span>
        </footer>
      </section>
    </main>
  );
}
