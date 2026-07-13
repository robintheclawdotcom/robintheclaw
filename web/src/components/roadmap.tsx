const milestones = [
  {
    id: "01",
    status: "shipped",
    title: "Mainnet foundation",
    summary: "Deploy the controlled base for strategy operations without authorizing capital.",
    items: [
      "Source-verified custody, mandate, routing, and governance contracts",
      "Halted, unfunded launch under Safe and timelock control",
      "Authenticated application, private API, and wallet onboarding",
      "Durable market capture and mainnet paper strategy services",
    ],
  },
  {
    id: "02",
    status: "active",
    title: "Live venue execution",
    summary: "Connect the agent to live venues for coordinated spot and perpetual trading.",
    items: [
      "Authenticated Lighter orders, fills, collateral, and positions",
      "Block-pinned spot quotes and live account-risk checks",
      "Coordinated entry, hedge, unwind, and funding workflows",
      "Fail-closed agent controls, bounded retries, and operator recovery",
    ],
  },
  {
    id: "03",
    status: "next",
    title: "Production evidence",
    summary: "Prove the live execution loop and close every control required for activation.",
    items: [
      "Independent venue, chain, account, and position reconciliation",
      "Benchmark, chaos, soak, replay, backup, and recovery evidence",
      "Production telemetry, archival retention, and external reviews",
      "Elapsed shadow windows and statistical promotion approval",
    ],
  },
  {
    id: "04",
    status: "gated",
    title: "Bounded activation",
    summary: "Start narrow, preserve human control, and expand only from verified evidence.",
    items: [
      "One approved market with explicit capital and exposure limits",
      "Canary execution with kill switches and independent reconciliation",
      "Measured entry, hedge, unwind, funding, and attribution quality",
      "Market and capacity expansion through separate promotion reviews",
    ],
  },
  {
    id: "05",
    status: "horizon",
    title: "Research advantage",
    summary: "Compound model quality without giving adaptive research direct execution authority.",
    items: [
      "Cointegration, Ornstein-Uhlenbeck, and Kalman hedge-ratio models",
      "Regime vetoes, shrinkage covariance, and portfolio capacity controls",
      "Execution-aware routing and private-order-flow analysis",
      "Isolated large-model research for hypotheses and post-trade review",
    ],
  },
] as const;

export default function Roadmap() {
  return (
    <div className="roadmap-view">
      <header className="roadmap-header">
        <div>
          <span className="roadmap-kicker">public roadmap · v1</span>
          <h1>From deployed infrastructure to bounded autonomy.</h1>
          <p>
            Robin advances when evidence closes a gate. Milestones are ordered by dependency,
            not promised dates.
          </p>
        </div>
        <div className="roadmap-state" aria-label="Current roadmap state">
          <span>current phase</span>
          <strong><i aria-hidden="true" /> live venue execution</strong>
          <small>live agent trading: in development</small>
        </div>
      </header>

      <div className="roadmap-principles" aria-label="Roadmap principles">
        <div><span>01</span><p>Evidence before promotion</p></div>
        <div><span>02</span><p>Fail closed by default</p></div>
        <div><span>03</span><p>Expand one boundary at a time</p></div>
      </div>

      <ol className="roadmap-list">
        {milestones.map((milestone) => (
          <li className={`roadmap-milestone status-${milestone.status}`} key={milestone.id}>
            <div className="roadmap-marker" aria-hidden="true">
              <span>{milestone.id}</span>
            </div>
            <article>
              <div className="roadmap-milestone-header">
                <div>
                  <span className="roadmap-status">{milestone.status}</span>
                  <h2>{milestone.title}</h2>
                </div>
                {milestone.status === "active" && <span className="roadmap-active-tag">in progress</span>}
              </div>
              <p>{milestone.summary}</p>
              <ul>
                {milestone.items.map((item) => <li key={item}>{item}</li>)}
              </ul>
            </article>
          </li>
        ))}
      </ol>

      <aside className="roadmap-note">
        <div>
          <span>release policy</span>
          <strong>Deployment is not activation.</strong>
        </div>
        <p>
          Contracts can be live while execution remains halted. Funding begins only after every
          empirical, operational, security, legal, and venue gate passes.
        </p>
        <a href="/docs">read the readiness specification →</a>
      </aside>
    </div>
  );
}
