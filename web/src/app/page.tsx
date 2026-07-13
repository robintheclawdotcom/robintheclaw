"use client";

import { useEffect, useRef, useState } from "react";
import { testnetProof } from "../lib/testnet-proof";

type DocId = "overview" | "signal" | "contracts" | "verifier" | "engine" | "testnet" | "architecture" | "developer" | "operations" | "security" | "venues";

const docs: Record<DocId, { file: string; title: string; body: React.ReactNode }> = {
  overview: {
    file: "README.md",
    title: "Overview",
    body: (
      <>
        <p>
          Robin the Claw is a bounded, delta-neutral RWA trading system for Robinhood Chain. It
          measures the spread between stock-token spot liquidity and matching RWA perpetuals,
          then builds a hedge plan only when the opportunity clears its risk gates.
        </p>
        <h2>The public contract</h2>
        <p>
          The system does not ask anyone to trust a screenshot. Trade batches are committed as
          Merkle roots so independently published records can be recomputed against the chain.
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
          It proves that a disclosed batch is the batch that was committed. It does not excuse
          withholding records or turn an unproven strategy into an investment product.
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
                  <p>Bounded, verifiable delta-neutral RWA trading on Robinhood Chain.</p>
                  <small>status: research → testnet foundation · market-neutral · quarter-Kelly</small>
                </div>
              </section>

              <section className="intro">
                <Prompt>robin init</Prompt>
                <p>
                  Robin the Claw measures the basis between tokenized-equity spot liquidity and
                  matching perpetuals, then evaluates a delta-neutral plan through liquidity,
                  sizing, and risk gates. The aim is not a returns promise. It is a system whose
                  record can be checked rather than trusted.
                </p>
              </section>

              <section className="clone">
                <Prompt>git clone</Prompt>
                <div className="clone-box">
                  <span>$</span>
                  <code>git clone https://github.com/robintheclawdotcom/robintheclaw.git</code>
                  <button onClick={copyClone}>{copied ? "copied ✓" : "copy"}</button>
                </div>
                <small>Open source trust surface · execution remains testnet-first.</small>
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
                  <article><span>[ signal ]</span><h2>Basis scanner</h2><p>Discovers v4 pools, compares spot with live perps, and records the spread before execution exists.</p></article>
                  <article><span>[ engine ]</span><h2>Decision gates</h2><p>Deterministic basis, sizing, risk, and neutrality checks turn an observation into a plan or reject it.</p></article>
                  <article><span>[ contracts ]</span><h2>Bounded custody</h2><p>An allowlisted, capped, haltable execution boundary limits what an agent may attempt.</p></article>
                  <article><span>[ verifier ]</span><h2>Recompute the record</h2><p>A live testnet proof confirms published records resolve to an on-chain commitment.</p></article>
                </div>
              </section>

              <section>
                <Prompt>robin explain --pipeline</Prompt>
                <div className="pipeline">
                  {[
                    ["01 · scan", "Measure a liquid, fresh basis"],
                    ["02 · size", "Fractional-Kelly, capped"],
                    ["03 · gate", "Risk limits and neutrality"],
                    ["04 · verify", "Commit and recompute records"],
                  ].map(([label, detail], index) => (
                    <div className="pipeline-item" key={label}>
                      <span>{label}</span><p>{detail}</p>{index < 3 && <b className="arrow">→</b>}
                    </div>
                  ))}
                </div>
              </section>

              <section className="docs-cta">
                <div><h2>Read the project notes</h2><p>Signal measurement, guardrails, decision engine, contracts, and verifier design.</p></div>
                <button onClick={openDocs}>robin docs →</button>
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
