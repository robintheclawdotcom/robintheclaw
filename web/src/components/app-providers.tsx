"use client";

import {
  PrivyProvider,
  toViemAccount,
  usePrivy,
  useWallets,
  type ConnectedWallet,
} from "@privy-io/react-auth";
import { robinhoodMainnet } from "@alchemy/common/chains";
import {
  alchemyWalletTransport,
  createSmartWalletClient,
} from "@alchemy/wallet-apis";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import type { Hex } from "viem";
import { AppApi } from "../lib/api";
import type { TransactionCall } from "../lib/app-types";

export type ConnectedAccount = {
  address: `0x${string}`;
  label: string;
  embedded: boolean;
};

type AuthContextValue = {
  configured: boolean;
  ready: boolean;
  authenticated: boolean;
  userId: string | null;
  hasRecovery: boolean;
  accounts: ConnectedAccount[];
  embeddedAddress: `0x${string}` | null;
  login: () => void;
  logout: () => Promise<void>;
  linkWallet: () => void;
  unlinkWallet: (address: string) => Promise<void>;
  linkEmail: () => void;
  linkPasskey: () => void;
  getAccessToken: () => Promise<string | null>;
};

type SmartWalletContextValue = {
  pending: boolean;
  executeCalls: (
    calls: TransactionCall[],
    signerAddress?: string,
    onSubmitted?: (callId: Hex) => void,
  ) => Promise<Hex>;
};

const AuthContext = createContext<AuthContextValue | null>(null);
const SmartWalletContext = createContext<SmartWalletContextValue | null>(null);
const ApiContext = createContext<AppApi | null>(null);

export function AppProviders({ children }: { children: React.ReactNode }) {
  const [queryClient] = useState(() => new QueryClient({
    defaultOptions: {
      queries: { staleTime: 10_000, retry: 1, refetchOnWindowFocus: false },
    },
  }));
  const appId = process.env.NEXT_PUBLIC_PRIVY_APP_ID;
  const mock = process.env.NEXT_PUBLIC_E2E_AUTH === "1";

  let content: React.ReactNode;
  if (mock) {
    content = <MockSession>{children}</MockSession>;
  } else if (appId) {
    content = (
      <PrivyProvider
        appId={appId}
        config={{
          loginMethods: ["email", "passkey", "google", "apple", "wallet"],
          supportedChains: [robinhoodMainnet],
          defaultChain: robinhoodMainnet,
          embeddedWallets: { ethereum: { createOnLogin: "all-users" } },
          appearance: {
            theme: "dark",
            accentColor: "#ccff00",
            logo: "/brand/icon-192.png",
            walletList: ["detected_wallets", "metamask", "phantom"],
          },
        }}
      >
        <LiveSession>{children}</LiveSession>
      </PrivyProvider>
    );
  } else {
    content = <UnconfiguredSession>{children}</UnconfiguredSession>;
  }

  return <QueryClientProvider client={queryClient}>{content}</QueryClientProvider>;
}

function LiveSession({ children }: { children: React.ReactNode }) {
  const privy = usePrivy();
  const privyRef = useRef(privy);
  privyRef.current = privy;
  const { wallets, ready: walletsReady } = useWallets();
  const [pending, setPending] = useState(false);
  const embeddedWallet = wallets.find((wallet) => wallet.walletClientType === "privy") ?? null;
  const getAccessToken = useCallback(() => privyRef.current.getAccessToken(), []);
  const syncedAuthentication = useRef<boolean | null>(null);

  const accounts = useMemo(() => wallets.map((wallet) => ({
    address: wallet.address as `0x${string}`,
    label: wallet.walletClientType === "privy" ? "Robin embedded wallet" : wallet.meta.name,
    embedded: wallet.walletClientType === "privy",
  })), [wallets]);
  const hasRecovery = Boolean(privy.user?.linkedAccounts.some((account) =>
    account.type === "email" || account.type === "passkey",
  ));

  useEffect(() => {
    if (!privy.ready || syncedAuthentication.current === privy.authenticated) return;
    syncedAuthentication.current = privy.authenticated;
    if (!privy.authenticated) {
      void fetch("/api/auth/session", { method: "DELETE" });
      return;
    }
    void getAccessToken().then((token) => {
      if (!token) return;
      return fetch("/api/auth/session", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ token }),
      });
    });
  }, [getAccessToken, privy.authenticated, privy.ready]);

  useEffect(() => {
    const expire = () => { void privy.logout(); };
    window.addEventListener("robin:session-expired", expire);
    return () => window.removeEventListener("robin:session-expired", expire);
  }, [privy]);

  const executeCalls = useCallback(async (
    calls: TransactionCall[],
    signerAddress?: string,
    onSubmitted?: (callId: Hex) => void,
  ) => {
    const wallet = signerAddress
      ? wallets.find((candidate) => candidate.address.toLowerCase() === signerAddress.toLowerCase())
      : embeddedWallet;
    if (!wallet) throw new Error("The selected wallet is not connected in this browser.");
    setPending(true);
    try {
      const signer = await toViemAccount({ wallet });
      const client = createSmartWalletClient({
        signer,
        chain: robinhoodMainnet,
        transport: alchemyWalletTransport({ url: "/api/wallet" }),
      });
      const result = await client.sendCalls({
        calls: calls.map((call) => ({
          to: call.to,
          data: call.data,
          value: BigInt(call.value),
        })),
      });
      onSubmitted?.(result.id);
      const status = await client.waitForCallsStatus({ id: result.id });
      if (status.status !== "success") throw new Error("The onchain operation did not complete.");
      return result.id;
    } finally {
      setPending(false);
    }
  }, [embeddedWallet, wallets]);

  const auth = useMemo<AuthContextValue>(() => ({
    configured: true,
    ready: privy.ready && walletsReady,
    authenticated: privy.authenticated,
    userId: privy.user?.id ?? null,
    hasRecovery,
    accounts,
    embeddedAddress: embeddedWallet?.address as `0x${string}` | null,
    login: () => privy.login(),
    logout: async () => { await privy.logout(); },
    linkWallet: () => privy.linkWallet({ walletChainType: "ethereum-only" }),
    unlinkWallet: async (address) => { await privy.unlinkWallet(address); },
    linkEmail: () => privy.linkEmail(),
    linkPasskey: () => privy.linkPasskey({ name: "Robin recovery" }),
    getAccessToken,
  }), [accounts, embeddedWallet?.address, getAccessToken, hasRecovery, privy, walletsReady]);

  return (
    <SessionContexts auth={auth} smartWallet={{ pending, executeCalls }}>
      {children}
    </SessionContexts>
  );
}

function MockSession({ children }: { children: React.ReactNode }) {
  const [authenticated, setAuthenticated] = useState(true);
  const [accounts, setAccounts] = useState<ConnectedAccount[]>([
    { address: "0x1111111111111111111111111111111111111111", label: "Robin embedded wallet", embedded: true },
    { address: "0x2222222222222222222222222222222222222222", label: "MetaMask", embedded: false },
  ]);
  useEffect(() => {
    if (window.localStorage.getItem("robin:e2e-auth") === "logged-out") setAuthenticated(false);
  }, []);
  useEffect(() => {
    const expire = () => setAuthenticated(false);
    window.addEventListener("robin:session-expired", expire);
    return () => window.removeEventListener("robin:session-expired", expire);
  }, []);
  const auth = useMemo<AuthContextValue>(() => ({
    configured: true,
    ready: true,
    authenticated,
    userId: authenticated ? "did:privy:test-user" : null,
    hasRecovery: true,
    accounts,
    embeddedAddress: "0x1111111111111111111111111111111111111111",
    login: () => { window.localStorage.removeItem("robin:e2e-auth"); setAuthenticated(true); },
    logout: async () => { window.localStorage.setItem("robin:e2e-auth", "logged-out"); setAuthenticated(false); },
    linkWallet: () => setAccounts((current) => current.some((wallet) => wallet.address === "0x3333333333333333333333333333333333333333") ? current : current.concat({ address: "0x3333333333333333333333333333333333333333", label: "Phantom", embedded: false })),
    unlinkWallet: async (address) => setAccounts((current) => current.filter((wallet) => wallet.address.toLowerCase() !== address.toLowerCase())),
    linkEmail: () => undefined,
    linkPasskey: () => undefined,
    getAccessToken: async () => "test-access-token",
  }), [accounts, authenticated]);
  const smartWallet = useMemo<SmartWalletContextValue>(() => ({
    pending: false,
    executeCalls: async (_calls, _signerAddress, onSubmitted) => {
      const callId = `0x${"ab".repeat(32)}` as Hex;
      onSubmitted?.(callId);
      return callId;
    },
  }), []);
  return <SessionContexts auth={auth} smartWallet={smartWallet}>{children}</SessionContexts>;
}

function UnconfiguredSession({ children }: { children: React.ReactNode }) {
  const auth = useMemo<AuthContextValue>(() => ({
    configured: false,
    ready: true,
    authenticated: false,
    userId: null,
    hasRecovery: false,
    accounts: [],
    embeddedAddress: null,
    login: () => undefined,
    logout: async () => undefined,
    linkWallet: () => undefined,
    unlinkWallet: async () => undefined,
    linkEmail: () => undefined,
    linkPasskey: () => undefined,
    getAccessToken: async () => null,
  }), []);
  const smartWallet = useMemo<SmartWalletContextValue>(() => ({
    pending: false,
    executeCalls: async () => { throw new Error("Application authentication is not configured."); },
  }), []);
  return <SessionContexts auth={auth} smartWallet={smartWallet}>{children}</SessionContexts>;
}

function SessionContexts({
  auth,
  smartWallet,
  children,
}: {
  auth: AuthContextValue;
  smartWallet: SmartWalletContextValue;
  children: React.ReactNode;
}) {
  const api = useMemo(() => new AppApi(auth.getAccessToken), [auth.getAccessToken]);
  return (
    <AuthContext.Provider value={auth}>
      <SmartWalletContext.Provider value={smartWallet}>
        <ApiContext.Provider value={api}>{children}</ApiContext.Provider>
      </SmartWalletContext.Provider>
    </AuthContext.Provider>
  );
}

export function useRobinAuth() {
  const context = useContext(AuthContext);
  if (!context) throw new Error("useRobinAuth must be used inside AppProviders.");
  return context;
}

export function useSmartWallet() {
  const context = useContext(SmartWalletContext);
  if (!context) throw new Error("useSmartWallet must be used inside AppProviders.");
  return context;
}

export function useAppApi() {
  const context = useContext(ApiContext);
  if (!context) throw new Error("useAppApi must be used inside AppProviders.");
  return context;
}

export type { ConnectedWallet };
