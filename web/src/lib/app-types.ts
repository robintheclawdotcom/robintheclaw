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
  status: "running" | "paused";
  createdAt: string;
  updatedAt: string;
};

export type AgentSnapshot = AgentRecord & {
  evaluations: number;
  candidates: number;
  lastEvaluatedAt: string | null;
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
