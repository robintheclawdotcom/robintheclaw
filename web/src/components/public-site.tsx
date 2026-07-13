"use client";

import { useEffect, useRef, useState } from "react";
import { robinhoodMainnetExplorer } from "../lib/chain";

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
          Robin the Claw is an autonomous delta-neutral trading stack for tokenized markets on
          Robinhood Chain: no-code strategy access, venue-native data, adaptive models, matched
          execution, and a durable operating layer for autonomous strategies.
        </p>
        <h2>From market structure to a trade plan</h2>
        <p>
          It turns continuous market and chain data into convergence research, regime-aware trade
          planning, and coordinated spot and perp legs.
        </p>
        <div className="note">Email or passkey opens a personal strategy account, linked wallets, and one dashboard.</div>
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
          The scanner discovers Uniswap v4 stock-token pools, compares their onchain spot prices
          with active Lighter perp marks, and feeds the market data layer for strategy research.
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
          The onchain foundation combines custody, delegated execution, venue policy, and
          operator control into a focused base for the Robin execution layer.
        </p>
        <h2>Built for expansion</h2>
        <p>
          The source-verified mainnet contract layer establishes typed custody, risk, routing, and
          governance boundaries for staged strategy activation.
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
          Robin can encode trade records deterministically, commit them onchain, and keep a
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
  engine: {
    file: "engine/README.md",
    title: "Trade planning engine",
    body: (
      <>
        <p>
          The Rust engine turns a basis observation into a matched spot and perp plan, combining
          market quality, fractional-Kelly sizing, portfolio awareness, and delta neutrality.
        </p>
        <h2>Built to evolve</h2>
        <p>
          The model roadmap adds convergence, regime, hedge-ratio, and portfolio layers while the
          execution engine stays focused on turning research into clear trade instructions.
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
    title: "Activation readiness",
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

export default function PublicSite({ view }: { view: "home" | "docs" }) {
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
                  <p>Delta-neutral market intelligence and execution for tokenized markets.</p>
                  <small>status: mainnet contract layer live → staged activation</small>
                </div>
              </section>

              <section className="intro">
                <Prompt>robin init</Prompt>
                <p>
                  Robin the Claw is an autonomous trading stack for tokenized markets. It captures
                  venue-native data, finds cross-venue basis, develops adaptive models, sizes a
                  position, and prepares matched spot and perp legs for execution. Users access it
                  through a no-code strategy account with a personal vault and unified dashboard.
                </p>
              </section>

              <section className="app-entry">
                <Prompt>robin open</Prompt>
                <div className="app-entry-card">
                  <div>
                    <span>✻</span>
                    <strong>Open your strategy account</strong>
                    <p>Sign in with email or passkey, create a personal vault, link funding wallets, and manage everything from one dashboard.</p>
                  </div>
                  <a href="/app">open app →</a>
                </div>
                <small>No extension, seed phrase, CLI, RPC setup, network switch, or gas balance required.</small>
              </section>

              <section className="proof-status">
                <Prompt>robin mainnet --status</Prompt>
                <div className="proof-panel">
                  <div>
                    <span className="proof-label">execution mainnet</span>
                    <strong>typed contract layer live · staged activation</strong>
                    <p>Source-verified governance, custody, risk, and routing contracts are live on Robinhood Chain mainnet.</p>
                  </div>
                  <a href={`${mainnetDeployment.explorer}/tx/${mainnetDeployment.transaction}`} target="_blank" rel="noreferrer">
                    inspect onchain ↗
                  </a>
                </div>
              </section>

              <section>
                <Prompt>robin --components</Prompt>
                <div className="cards">
                  <article><span>[ signal ]</span><h2>Market intelligence</h2><p>Discovers v4 pools, compares spot with live perps, and maps the basis across tokenized markets.</p></article>
                  <article><span>[ app ]</span><h2>No-code strategy access</h2><p>Opens a personal strategy account, linked wallets, real balances, controls, and activity in one dashboard.</p></article>
                  <article><span>[ engine ]</span><h2>Trade planning</h2><p>Combines spread quality, Kelly sizing, portfolio awareness, and neutrality into a coordinated plan.</p></article>
                  <article><span>[ research ]</span><h2>Adaptive research</h2><p>Builds a compounding event store for convergence, regime, hedge-ratio, and routing models.</p></article>
                  <article><span>[ contracts ]</span><h2>Execution foundation</h2><p>Source-verified mainnet custody, risk, routing, and governance establish the Robin execution layer.</p></article>
                  <article><span>[ verifier ]</span><h2>Record integrity</h2><p>Onchain records support inspection, research, and a durable history of strategy operations.</p></article>
                </div>
              </section>

              <section>
                <Prompt>robin explain --pipeline</Prompt>
                <div className="pipeline">
                  {[
                    ["01 · capture", "Build venue-native market and chain history"],
                    ["02 · model", "Find convergence and regime-aware opportunities"],
                    ["03 · plan", "Size matched spot and perp legs"],
                    ["04 · learn", "Refine hypotheses, routing, and execution"],
                  ].map(([label, detail], index) => (
                    <div className="pipeline-item" key={label}>
                      <span>{label}</span><p>{detail}</p>{index < 3 && <b className="arrow">→</b>}
                    </div>
                  ))}
                </div>
              </section>

              <section className="docs-cta">
                <div><h2>Explore Robin</h2><p>No-code strategy access, market intelligence, adaptive models, trade planning, contracts, and operations.</p></div>
                <div className="docs-cta-actions"><a href="/app">open app →</a><a className="docs-link" href="/docs">docs</a></div>
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
