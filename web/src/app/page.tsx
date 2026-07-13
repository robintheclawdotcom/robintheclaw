"use client";

import { useEffect, useRef, useState } from "react";
import { testnetProof } from "../lib/testnet-proof";

type DocId = "overview" | "signal" | "contracts" | "verifier" | "engine" | "testnet" | "methodology" | "architecture" | "developer" | "operations" | "security" | "venues";

const docs: Record<DocId, { file: string; title: string; body: React.ReactNode }> = {
  overview: {
    file: "README.md",
    title: "Overview",
    body: (
      <>
        <p>
          Robin the Claw is a delta-neutral RWA trading agent for Robinhood Chain. It targets
          repeatable, risk-adjusted returns from stock-token spot and perpetual basis, building a
          hedge plan only when an opportunity clears cost, liquidity, and risk gates.
        </p>
        <h2>Measured performance</h2>
        <p>
          Trade batches are committed as Merkle roots so published results can be recomputed
          against the chain. The resulting record gives each disclosed result a reproducible source
          of truth.
        </p>
        <div className="note">
          Market-neutral is not risk-free. Basis can widen, funding can invert, and a non-atomic
          two-leg execution can leave temporary exposure. The system is being built testnet-first.
        </div>
      </>
    ),
  },
  signal: {
    file: "signal/README.md",
    title: "Signal measurement",
    body: (
      <>
        <p>
          The scanner discovers Uniswap v4 stock-token pools, compares their on-chain spot prices
          with active Lighter perp marks, and writes a time series before any execution is enabled.
        </p>
        <h2>What qualifies</h2>
        <p>
          A candidate must be fresh, liquid enough to be meaningful, and wide enough to survive
          fees, slippage, and the uncertainty of legging two venues. A large number on a shallow
          AMM is a stale mark, not an opportunity.
        </p>
        <div className="code-block"><span>$</span> node signal/src/spot.mjs</div>
      </>
    ),
  },
  contracts: {
    file: "contracts/README.md",
    title: "Bounded execution",
    body: (
      <>
        <p>
          The current on-chain foundation is a single-owner custody boundary with an agent key,
          target-and-selector allowlist, rolling notional ceiling, and human-controlled halt.
        </p>
        <h2>Current scope</h2>
        <p>
          It is not an ERC-4626 public vault and it does not execute live trades. The testnet
          proof deployment has no allowed venue at all; position accounting, actual-outflow
          enforcement, and a verified perp adapter remain required before any execution path.
        </p>
        <div className="code-block"><span>$</span> cd contracts && forge test -vv</div>
      </>
    ),
  },
  verifier: {
    file: "verifier/README.md",
    title: "Recompute the record",
    body: (
      <>
        <p>
          A published trade log is encoded deterministically, turned into an order-preserving
          Merkle tree, and matched to the root anchored on chain. Altering a fill, amount, or
          sequence produces a different root and fails verification.
        </p>
        <h2>What it proves</h2>
        <p>
          It proves that a disclosed batch is the batch that was committed. It supports honest
          performance analysis; it does not excuse withholding records or validate an unproven edge.
        </p>
        <div className="code-block"><span>$</span> cd verifier && npm test</div>
      </>
    ),
  },
  testnet: {
    file: "deployments/testnet-proof.json",
    title: "Testnet proof",
    body: (
      <>
        <p>
          The deployed testnet vault has no allowlisted execution target. Its first anchored batch
          is a disclosed synthetic fixture that proves only the custody-to-attestation-to-verifier
          path; it is not a fill, position, or performance record.
        </p>
        <h2>Independent check</h2>
        <p>
          The public root can be read from the anchor and reproduced from the tracked fixture with
          the verifier command. The record and the chain commitment must agree exactly.
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
    title: "Decision engine",
    body: (
      <>
        <p>
          The Rust engine is deterministic: it evaluates a basis observation, applies
          fractional-Kelly sizing, clears the result through exposure and drawdown limits, and
          outputs matched spot and perp legs only when every gate passes.
        </p>
        <h2>Execution stays separate</h2>
        <p>
          The engine does not hold keys, call venues, or mutate portfolio state. That separation
          makes plans reproducible and keeps the eventual executor narrow and auditable.
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
                  <p>Systematic RWA basis trading with disciplined risk.</p>
                  <small>research stage · market-neutral basis · fractional Kelly</small>
                </div>
              </section>

              <section className="intro">
                <Prompt>robin init</Prompt>
                <p>
                  Robin the Claw targets repeatable, risk-adjusted returns from tokenized-equity
                  basis and funding. It measures executable spot and perpetual prices, models the
                  full cost of a hedge, and advances only opportunities that clear liquidity,
                  sizing, and risk controls. Every disclosed result remains independently reviewable.
                </p>
              </section>

              <section className="clone">
                <Prompt>git clone</Prompt>
                <div className="clone-box">
                  <span>$</span>
                  <code>git clone https://github.com/robintheclawdotcom/robintheclaw.git</code>
                  <button onClick={copyClone}>{copied ? "copied ✓" : "copy"}</button>
                </div>
                <small>Open-source research and verification surface · execution remains testnet-first.</small>
              </section>

              <section className="proof-status">
                <Prompt>robin verify --testnet</Prompt>
                <div className="proof-panel">
                  <div>
                    <span className="proof-label">testnet proof</span>
                    <strong>verified synthetic batch · sequence {testnetProof.sequence}</strong>
                    <p>Anchor, vault, and verifier agree. No execution venue is allowlisted.</p>
                  </div>
                  <a href={`${testnetProof.explorer}/tx/${testnetProof.transaction}`} target="_blank" rel="noreferrer">
                    inspect on chain ↗
                  </a>
                </div>
              </section>

              <section>
                <Prompt>robin --components</Prompt>
                <div className="cards">
                  <article><span>[ signal ]</span><h2>Basis scanner</h2><p>Maps v4 pools, compares executable spot and perpetual prices, and measures whether a spread survives real costs.</p></article>
                  <article><span>[ engine ]</span><h2>Decision gates</h2><p>Converts validated observations into matched hedge plans within explicit sizing, exposure, and drawdown limits.</p></article>
                  <article><span>[ research ]</span><h2>Model hierarchy</h2><p>Tests convergence, regimes, portfolio construction, and execution assumptions against promotion gates.</p></article>
                  <article><span>[ contracts ]</span><h2>Bounded custody</h2><p>Enforces an allowlisted, capped, haltable boundary around capital and execution authority.</p></article>
                  <article><span>[ verifier ]</span><h2>Recompute the record</h2><p>Makes every disclosed trade batch reproducible from its committed on-chain record.</p></article>
                </div>
              </section>

              <section>
                <Prompt>robin explain --pipeline</Prompt>
                <div className="pipeline">
                  {[
                    ["01 · observe", "Capture executable market state"],
                    ["02 · model", "Estimate net edge and capacity"],
                    ["03 · shadow", "Replay fills across market regimes"],
                    ["04 · promote", "Advance only validated strategies"],
                  ].map(([label, detail], index) => (
                    <div className="pipeline-item" key={label}>
                      <span>{label}</span><p>{detail}</p>{index < 3 && <b className="arrow">→</b>}
                    </div>
                  ))}
                </div>
              </section>

              <section className="docs-cta">
                <div><h2>Explore the methodology</h2><p>Research standards, execution controls, and the path from market evidence to live capital.</p></div>
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
