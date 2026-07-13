"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { ErrorNotice, LoadingPanel, PageHeader } from "../../../components/app-ui";
import { useAppApi, useRobinAuth } from "../../../components/app-providers";
import { AppApiError } from "../../../lib/api";
import { formatAddress, formatDate } from "../../../lib/format";

export default function WalletsPage() {
  const api = useAppApi();
  const auth = useRobinAuth();
  const queryClient = useQueryClient();
  const [conflict, setConflict] = useState<Error>();
  const [linkError, setLinkError] = useState<unknown>();
  const accountCount = useRef(auth.accounts.length);
  const query = useQuery({ queryKey: ["me"], queryFn: () => api.me() });
  const sync = useMutation({
    mutationFn: () => api.syncWallets(),
    onSuccess: (data) => { setConflict(undefined); queryClient.setQueryData(["me"], data); void api.metric("wallet_sync", undefined, "success").catch(() => undefined); },
    onError: (error) => { setConflict(error); void api.metric("wallet_sync", undefined, "conflict").catch(() => undefined); },
  });
  const link = useMutation({
    mutationFn: async () => { await auth.linkWallet(); return api.syncWallets(); },
    onSuccess: (data) => { setConflict(undefined); setLinkError(undefined); queryClient.setQueryData(["me"], data); void api.metric("wallet_link", undefined, "success").catch(() => undefined); },
    onError: (error) => {
      setLinkError(error);
      setConflict(error instanceof AppApiError && error.status === 409 ? error : undefined);
      void api.metric("wallet_link", undefined, "failed").catch(() => undefined);
    },
  });
  const preferences = useMutation({
    mutationFn: (address: string) => {
      if (!query.data) throw new Error("Account preferences are not ready.");
      return api.updatePreferences({
        displayCurrency: query.data.preferences.displayCurrency,
        activeFundingWallet: address,
        notificationsEnabled: query.data.preferences.notificationsEnabled,
      });
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["me"] }),
  });
  const unlink = useMutation({
    mutationFn: async (address: string) => { await auth.unlinkWallet(address); return api.syncWallets(); },
    onSuccess: (data) => queryClient.setQueryData(["me"], data),
  });

  useEffect(() => {
    if (accountCount.current === auth.accounts.length) return;
    accountCount.current = auth.accounts.length;
    sync.mutate();
  }, [auth.accounts.length, sync]);

  if (query.isLoading) return <LoadingPanel label="Loading linked wallets…" />;
  if (query.error || !query.data) return <ErrorNotice error={query.error} retry={() => void query.refetch()} />;
  const me = query.data;

  return (
    <>
      <PageHeader eyebrow="Capital" title="Wallets" description="Manage verified funding sources and portfolio connections. Vault ownership remains unchanged." action={<button className="button button-primary" disabled={link.isPending} onClick={() => link.mutate()}>{link.isPending ? "Linking…" : "Link wallet"}</button>} />
      {conflict && (
        <div className="notice notice-error account-conflict" role="alert"><div><strong>Wallet already linked</strong><p>This address is linked to another Robin account. Sign in to that account to manage it.</p></div><button className="button button-secondary" onClick={() => void auth.logout()}>Sign in to other account</button></div>
      )}
      <section className="panel wallet-panel">
        <div className="panel-heading"><div><span className="eyebrow">Linked portfolio</span><h2>{me.wallets.length} wallet{me.wallets.length === 1 ? "" : "s"}</h2></div><button className="button button-quiet" disabled={sync.isPending} onClick={() => sync.mutate()}>{sync.isPending ? "Syncing…" : "Sync wallets"}</button></div>
        <div className="wallet-list">
          {me.wallets.map((wallet) => {
            const active = me.preferences.activeFundingWallet?.toLowerCase() === wallet.address.toLowerCase();
            return (
              <article key={wallet.id}>
                <div className={`wallet-avatar ${wallet.walletType}`}>{wallet.walletType === "embedded" ? "R" : "W"}</div>
                <div className="wallet-identity"><strong>{wallet.label ?? (wallet.walletType === "embedded" ? "Robin embedded wallet" : "External wallet")}</strong><span>{formatAddress(wallet.address)}</span><small>Verified {formatDate(wallet.verifiedAt)}</small></div>
                <div className="wallet-actions">
                  {wallet.isPrimary ? <span className="status-pill">Vault owner</span> : <button className={`button ${active ? "button-active" : "button-secondary"}`} disabled={active || preferences.isPending} onClick={() => preferences.mutate(wallet.address)}>{active ? "Funding wallet" : "Use for funding"}</button>}
                  {!wallet.isPrimary && <button className="button button-quiet danger" disabled={unlink.isPending} onClick={() => unlink.mutate(wallet.address)}>Unlink</button>}
                </div>
              </article>
            );
          })}
        </div>
      </section>
      {(sync.error && !conflict) && <ErrorNotice error={sync.error} />}
      {(linkError && !conflict) && <ErrorNotice error={linkError} />}
      {preferences.error && <ErrorNotice error={preferences.error} />}
      {unlink.error && <ErrorNotice error={unlink.error} />}
      <section className="panel ownership-note"><span className="lock-mark">⌁</span><div><h2>Vault ownership</h2><p>The smart account remains the sole vault owner. Funding-wallet changes cannot rotate ownership or alter withdrawal authorization.</p><strong>{formatAddress(me.smartAccount?.address)}</strong></div></section>
    </>
  );
}
