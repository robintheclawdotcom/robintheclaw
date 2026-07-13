export type Amount = {
  raw: string;
  decimals: number;
  symbol: string;
};

export type UserRecord = {
  id: string;
  privyDid: string;
  onboardingState: "account" | "recovery" | "vault" | "complete";
  hasRecovery: boolean;
  createdAt: string;
  updatedAt: string;
};

export type WalletRecord = {
  id: string;
  chainNamespace: string;
  address: `0x${string}`;
  walletType: "embedded" | "external" | "smart";
  label: string | null;
  isPrimary: boolean;
  verifiedAt: string;
};

export type SmartAccountRecord = {
  chainId: number;
  address: `0x${string}`;
  provider: string;
  createdAt: string;
};

export type PreferencesRecord = {
  displayCurrency: "USD" | "EUR" | "GBP";
  activeFundingWallet: `0x${string}` | null;
  notificationsEnabled: boolean;
  updatedAt: string;
};

export type VaultRecord = {
  id: string;
  chainId: number;
  factoryVersion: number;
  assetAddress: `0x${string}`;
  vaultAddress: `0x${string}`;
  guardAddress: `0x${string}`;
  anchorAddress: `0x${string}`;
  callId: `0x${string}`;
  transactionHash: `0x${string}`;
  status: string;
  createdAt: string;
  updatedAt: string;
};

export type ActivityRecord = {
  id: string;
  chainId: number;
  kind: string;
  transactionHash: `0x${string}` | null;
  blockNumber: number | null;
  logIndex: number | null;
  payload: Record<string, unknown>;
  occurredAt: string;
};

export type MeResponse = {
  user: UserRecord;
  wallets: WalletRecord[];
  smartAccount: SmartAccountRecord | null;
  preferences: PreferencesRecord;
  vault: VaultRecord | null;
};

export type VaultSnapshot = {
  record: VaultRecord;
  balance: Amount;
  halted: boolean;
  remainingCapacity: Amount;
};

export type PositionSnapshot = {
  id: string;
  symbol: string;
  status: string;
  spotLeg: Amount;
  perpLeg: Amount;
  entryBasisBps: string;
  currentBasisBps: string;
  funding: Amount;
  pnl: Amount;
};

export type OpportunitySnapshot = {
  symbol: string;
  basisBps: string;
  liquidity: string;
  observedAt: number;
};

export type AgentRecord = {
  id: string;
  strategyVersion: string;
  mode: "paper" | "live";
  status: AgentStatus;
  createdAt: string;
  updatedAt: string;
};

export type AgentStatus =
  | "setup"
  | "provisioning"
  | "awaiting_signatures"
  | "awaiting_funding"
  | "ready"
  | "running"
  | "reducing"
  | "paused"
  | "closing"
  | "closed"
  | "blocked";

export type AgentSnapshot = AgentRecord & {
  evaluations: number;
  candidates: number;
  lastEvaluatedAt: string | null;
};

export type ExecutionAccountRecord = {
  id: string;
  agentId: string;
  strategyVersion: "basis-aapl-v1";
  strategyManifestSha256: string;
  chainId: 4663;
  status: "provisioning" | "awaiting_signatures" | "awaiting_funding" | "ready" | "blocked" | "closed";
  createdAt: string;
  updatedAt: string;
};

export type ExecutionBindingRecord = {
  bindingRef: string;
  requestId: string;
  providerRequestId: string | null;
  venue: "lighter" | "robinhood";
  ownerAddress: string;
  lighterAccountIndex: number | null;
  lighterApiKeyIndex: number | null;
  robinhoodVaultAddress: string | null;
  robinhoodSignerAddress: string | null;
  robinhoodKeyVersion: number | null;
  robinhoodFactoryAddress: string | null;
  robinhoodRegistryAddress: string | null;
  robinhoodPolicyDigest: string | null;
  robinhoodRiskManagerAddress: string | null;
  robinhoodSpotAdapterAddress: string | null;
  robinhoodDeploymentBlock: number | null;
  robinhoodDeploymentAction: RobinhoodDeploymentAction | null;
  publicIdentifier: string | null;
  publicKey: string | null;
  associationPayload: string | null;
  proofTransactionHash: string | null;
  status: "provisioning" | "awaiting_signature" | "verifying" | "linked" | "rejected";
  createdAt: string;
  updatedAt: string;
};

export type AgentReadiness = {
  executionAccountId: string;
  lighterAccountIndex: number | null;
  robinhoodOwnerAddress: string | null;
  robinhoodVaultAddress: string | null;
  robinhoodSignerAddress: string | null;
  coordinatorRegistered: boolean;
  lighterLinked: boolean;
  lighterFunded: boolean;
  robinhoodDeployed: boolean;
  robinhoodFunded: boolean;
  userGasReady: boolean;
  executionGasReady: boolean;
  policyActive: boolean;
  reconciled: boolean;
  validUntil: string | null;
  canLaunch: boolean;
  blockers: string[];
};

export type AgentCommand = "launch" | "pause" | "resume" | "close" | "withdraw";

export type AgentCommandRecord = {
  id: string;
  agentId: string;
  executionAccountId: string;
  idempotencyKey: string;
  command: AgentCommand;
  status: "pending" | "processing" | "awaiting_signature" | "completed" | "rejected" | "failed";
  agentStatus: AgentStatus;
  targetAgentStatus: AgentStatus;
  errorReason: string | null;
  resultEvidenceDigest: string | null;
  ownerActions: OwnerAction[];
  completedAt: string | null;
  createdAt: string;
  updatedAt: string;
};

export type RobinhoodDeploymentAction = {
  kind: "deploy_user_graph";
  chainId: "4663";
  to: `0x${string}`;
  data: `0x${string}`;
  value: "0";
};

export type OwnerAction = {
  chain_id: 4663;
  from: `0x${string}`;
  to: `0x${string}`;
  data: `0x${string}`;
  value: "0";
};

export type DashboardSnapshot = {
  environment: string;
  asOf: string;
  infrastructureReady: boolean;
  agent: AgentSnapshot | null;
  totalValue: Amount;
  availableBalance: Amount;
  deployedCapital: Amount;
  pnl: Amount | null;
  smartAccount: SmartAccountRecord | null;
  vault: VaultSnapshot | null;
  positions: PositionSnapshot[];
  opportunities: OpportunitySnapshot[];
  activity: ActivityRecord[];
  wallets: Array<{ wallet: WalletRecord; balance: Amount }>;
};

export type TransactionCall = {
  to: `0x${string}`;
  data: `0x${string}`;
  value: string;
};

export type TransactionPlan = {
  chainId: number;
  smartAccount: `0x${string}`;
  expectedVault: `0x${string}`;
  calls: TransactionCall[];
};

export type ActivityPage = {
  items: ActivityRecord[];
  nextCursor: string | null;
};
