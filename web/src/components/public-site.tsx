"use client";

import { useEffect, useRef, useState } from "react";
import { robinhoodMainnetExplorer } from "../lib/chain";
import Roadmap from "./roadmap";

const mainnetDeployment = {
  explorer: robinhoodMainnetExplorer,
  transaction: "0xe8b7ca77feaf117e287eab146d7e79bdef83737a93453534bc9077da0e0ac961",
};

type DocId = "overview" | "experience" | "signal" | "contracts" | "verifier" | "engine" | "mainnet" | "methodology" | "research" | "execution" | "data" | "infrastructure" | "control" | "readiness" | "venue";

const docs: Record<DocId, { file: string; title: string; body: React.ReactNode }> = {
  overview: {
    file: "README.md",
    title: "Overview",
    body: (
      <>
        <p>
          Robin the Claw is an autonomous, delta-neutral trading system for tokenized markets on
          Robinhood Chain. The platform combines venue-native market data, proprietary research,
          portfolio-aware sizing, coordinated execution, and onchain custody.
        </p>
        <h2>From signal to settlement</h2>
        <p>
          Continuous spot, perpetual, and chain data drive executable basis estimates, regime
          controls, and matched orders.
        </p>
        <div className="note">The operator interface consolidates capital, exposure, positions, activity, and mandate controls.</div>
      </>
    ),
  },
  experience: {
    file: "docs/user-experience.md",
    title: "User experience",
    body: <PublishedDoc file="user-experience.md" />,
  },
  signal: {
    file: "signal/README.md",
    title: "Market intelligence",
    body: (
      <>
        <p>
          The market data layer normalizes Uniswap v4 spot liquidity and Lighter perpetual markets
          for research and execution.
        </p>
        <h2>Executable edge</h2>
        <p>
          Every opportunity is evaluated against depth, fees, funding, gas, latency, and quote
          freshness.
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
          The non-upgradeable contract system separates custody, mandate enforcement, routing, and
          administration.
        </p>
        <h2>Typed execution boundaries</h2>
        <p>
          The source-verified mainnet deployment constrains assets, routes, recipients, limits, and
          operating state.
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
          Robin produces deterministic records for orders, execution, funding, and P&amp;L
          attribution.
        </p>
        <h2>Audit-grade lineage</h2>
        <p>
          Onchain commitments bind operational data to immutable roots for reconciliation and
          independent analysis.
        </p>
        <div className="code-block"><span>$</span> cd verifier && npm test</div>
      </>
    ),
  },
  engine: {
    file: "engine/README.md",
    title: "Trade planning engine",
    body: (
      <>
        <p>
          The deterministic Rust engine converts market observations into matched spot and
          perpetual plans using net edge, fractional Kelly, portfolio constraints, and delta
          targets.
        </p>
        <h2>Research in, orders out</h2>
        <p>
          Research artifacts inform sizing and risk while the planning path remains deterministic.
        </p>
        <div className="code-block"><span>$</span> cd engine && cargo test</div>
      </>
    ),
  },
  mainnet: {
    file: "docs/mainnet-deployment.md",
    title: "Mainnet deployment",
    body: <PublishedDoc file="mainnet-deployment.md" />,
  },
  methodology: {
    file: "docs/research-methodology.md",
    title: "Edge research methodology",
    body: <PublishedDoc file="research-methodology.md" />,
  },
  research: {
    file: "docs/research-runtime.md",
    title: "Research runtime",
    body: <PublishedDoc file="research-runtime.md" />,
  },
  execution: {
    file: "docs/execution-control-plane.md",
    title: "Execution control plane",
    body: <PublishedDoc file="execution-control-plane.md" />,
  },
  data: {
    file: "docs/data-plane-archive.md",
    title: "Data archive",
    body: <PublishedDoc file="data-plane-archive.md" />,
  },
  infrastructure: {
    file: "docs/infrastructure-readiness.md",
    title: "Infrastructure",
    body: <PublishedDoc file="infrastructure-readiness.md" />,
  },
  control: {
    file: "docs/control-plane-operations.md",
    title: "Operator control plane",
    body: <PublishedDoc file="control-plane-operations.md" />,
  },
  readiness: {
    file: "docs/production-audit-full-system.md",
    title: "Live execution readiness",
    body: <PublishedDoc file="production-audit-full-system.md" />,
  },
  venue: {
    file: "docs/venue-lighter.md",
    title: "Lighter venue integration",
    body: <PublishedDoc file="venue-lighter.md" />,
  },
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

function ThemeToggle({
  theme,
  onToggle,
  className = "",
}: {
  theme: "dark" | "light";
  onToggle: () => void;
  className?: string;
}) {
  const next = theme === "dark" ? "light" : "dark";

  return (
    <button
      type="button"
      className={`theme-toggle ${className}`.trim()}
      onClick={onToggle}
      role="switch"
      aria-checked={theme === "light"}
      aria-label={`Use ${next} theme`}
    >
      <span className="theme-track" aria-hidden="true"><span className="theme-thumb" /></span>
      <span className="theme-label">{theme}</span>
    </button>
  );
}

export default function PublicSite({ view }: { view: "home" | "docs" | "roadmap" }) {
  const [theme, setTheme] = useState<"dark" | "light">("dark");
  const [doc, setDoc] = useState<DocId>("overview");
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

  const selectDoc = (id: DocId) => {
    setDoc(id);
    setMenuOpen(false);
  };

  return (
    <main data-theme={theme}>
      <section className="terminal">
        <header className="titlebar">
          <div className="window-controls" aria-hidden="true"><span /><span /><span /></div>
          <nav className="desktop-nav" aria-label="Primary navigation">
            <a className={view === "home" ? "nav-active" : ""} href="/">home</a>
            <a className={view === "roadmap" ? "nav-active" : ""} href="/roadmap">roadmap</a>
            <a className={view === "docs" ? "nav-active" : ""} href="/docs">docs</a>
            <a className="open-app-link" href="/app">open app</a>
          </nav>
          <div className="terminal-title">robin@claw · /public · zsh</div>
          <div className="desktop-actions">
            <ThemeToggle theme={theme} onToggle={toggleTheme} />
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
                <a className={view === "home" ? "active" : ""} href="/">home</a>
                <a className={view === "roadmap" ? "active" : ""} href="/roadmap">roadmap</a>
                <a className={view === "docs" ? "active" : ""} href="/docs">docs</a>
                <a className="drawer-app-link" href="/app">open app</a>
                <ThemeToggle theme={theme} onToggle={toggleTheme} className="drawer-theme" />
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
                  <p>Autonomous, delta-neutral trading for tokenized markets.</p>
                  <small>mainnet contract system: deployed</small>
                </div>
              </section>

              <section className="intro">
                <Prompt>robin init</Prompt>
                <p>
                  Robin the Claw is an autonomous trading system for cross-venue basis in tokenized
                  markets. It combines venue-native data, adaptive research,
                  portfolio-aware sizing, and coordinated spot and perpetual execution.
                </p>
              </section>

              <section className="app-entry">
                <Prompt>robin operate</Prompt>
                <div className="app-entry-card">
                  <div>
                    <span>✻</span>
                    <strong>Strategy operations</strong>
                    <p>Monitor capital, exposure, execution, and performance from one control surface.</p>
                  </div>
                  <a href="/app">open app →</a>
                </div>
                <small>Account abstraction removes the need for wallet extensions, manual RPC configuration, and gas management.</small>
              </section>

              <section className="proof-status">
                <Prompt>robin mainnet --status</Prompt>
                <div className="proof-panel">
                  <div>
                    <span className="proof-label">mainnet infrastructure</span>
                    <strong>production contract system deployed</strong>
                    <p>Custody, governance, mandate enforcement, and spot routing are live on Robinhood Chain.</p>
                  </div>
                  <a href={`${mainnetDeployment.explorer}/tx/${mainnetDeployment.transaction}`} target="_blank" rel="noreferrer">
                    inspect onchain ↗
                  </a>
                </div>
              </section>

              <section>
                <Prompt>robin --components</Prompt>
                <div className="cards">
                  <article><span>[ signal ]</span><h2>Market intelligence</h2><p>Streams spot and perpetual markets to identify actionable cross-venue dislocations.</p></article>
                  <article><span>[ app ]</span><h2>Strategy operations</h2><p>Consolidates capital, exposure, controls, performance, and linked wallets.</p></article>
                  <article><span>[ engine ]</span><h2>Portfolio construction</h2><p>Converts validated signals into portfolio-aware, delta-neutral trade plans.</p></article>
                  <article><span>[ research ]</span><h2>Adaptive research</h2><p>Builds point-in-time datasets for convergence, regime, hedge-ratio, and execution models.</p></article>
                  <article><span>[ contracts ]</span><h2>Execution controls</h2><p>Enforces custody, mandate, routing, and governance through source-verified contracts.</p></article>
                  <article><span>[ verifier ]</span><h2>Record integrity</h2><p>Anchors deterministic execution records onchain for attribution, audit, and research.</p></article>
                </div>
              </section>

              <section>
                <Prompt>robin explain --pipeline</Prompt>
                <div className="pipeline">
                  {[
                    ["01 · capture", "Observe market and chain microstructure"],
                    ["02 · model", "Estimate executable, regime-adjusted edge"],
                    ["03 · plan", "Size coordinated spot and perpetual exposure"],
                    ["04 · learn", "Measure execution quality and model decay"],
                  ].map(([label, detail], index) => (
                    <div className="pipeline-item" key={label}>
                      <span>{label}</span><p>{detail}</p>{index < 3 && <b className="arrow">→</b>}
                    </div>
                  ))}
                </div>
              </section>

              <section className="docs-cta">
                <div><h2>Explore the system</h2><p>Architecture, research, execution, contracts, and operations.</p></div>
                <div className="docs-cta-actions"><a href="/app">open app →</a><a className="docs-link" href="/docs">docs</a></div>
              </section>
            </div>
          ) : view === "docs" ? (
            <div className="docs-view">
              <aside><DocsTree doc={doc} onSelect={selectDoc} /></aside>
              <article className="doc-article">
                <small>{docs[doc].file}</small>
                <h1>{docs[doc].title}</h1>
                {docs[doc].body}
              </article>
            </div>
          ) : (
            <Roadmap />
          )}
        </div>

        <footer>
          <span>© 2026 Robin the Claw · built on Robinhood Chain</span>
          <span><a href="/roadmap">roadmap</a><a href="https://github.com/robintheclawdotcom/robintheclaw">github</a><a href="https://x.com/RobinTheClaw">x / twitter</a></span>
        </footer>
      </section>
    </main>
  );
}
