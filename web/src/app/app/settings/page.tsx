"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ErrorNotice, LoadingPanel, PageHeader } from "../../../components/app-ui";
import { useAppApi, useRobinAuth } from "../../../components/app-providers";

export default function SettingsPage() {
  const api = useAppApi();
  const auth = useRobinAuth();
  const queryClient = useQueryClient();
  const query = useQuery({ queryKey: ["me"], queryFn: () => api.me() });
  const update = useMutation({
    mutationFn: (input: { displayCurrency: string; notificationsEnabled: boolean }) => {
      if (!query.data) throw new Error("Preferences are not ready.");
      return api.updatePreferences({ ...input, activeFundingWallet: query.data.preferences.activeFundingWallet });
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["me"] }),
  });

  if (query.isLoading) return <LoadingPanel />;
  if (query.error || !query.data) return <ErrorNotice error={query.error} retry={() => void query.refetch()} />;
  const preferences = query.data.preferences;
  return (
    <>
      <PageHeader eyebrow="Settings" title="Account preferences" description="Recovery, display, notification, and session controls for your Robin account." />
      <div className="settings-grid">
        <section className="panel settings-card"><div><span className="eyebrow">Recovery</span><h2>{query.data.user.hasRecovery || auth.hasRecovery ? "Recovery connected" : "Add account recovery"}</h2><p>Email or passkey lets you restore the same Robin account and smart-account address after logout.</p></div><div className="button-row"><button className="button button-secondary" onClick={auth.linkEmail}>Add email</button><button className="button button-secondary" onClick={auth.linkPasskey}>Add passkey</button></div></section>
        <section className="panel settings-card"><div><span className="eyebrow">Display</span><h2>Currency</h2><p>Choose the reference currency used when fiat values are available.</p></div><label htmlFor="display-currency">Display currency</label><select id="display-currency" value={preferences.displayCurrency} onChange={(event) => update.mutate({ displayCurrency: event.target.value, notificationsEnabled: preferences.notificationsEnabled })}><option>USD</option><option>EUR</option><option>GBP</option></select></section>
        <section className="panel settings-card"><div><span className="eyebrow">Notifications</span><h2>Strategy updates</h2><p>Control account and strategy notifications. Transaction prompts always remain visible.</p></div><label className="toggle-row"><input type="checkbox" checked={preferences.notificationsEnabled} onChange={(event) => update.mutate({ displayCurrency: preferences.displayCurrency, notificationsEnabled: event.target.checked })} /><span>Enable notifications</span></label></section>
        <section className="panel settings-card session-card"><div><span className="eyebrow">Session</span><h2>Sign out safely</h2><p>Your vault and linked-wallet records remain attached to your durable Robin account.</p></div><button className="button button-secondary" onClick={() => void auth.logout()}>Sign out</button></section>
      </div>
      {update.error && <ErrorNotice error={update.error} />}
    </>
  );
}
