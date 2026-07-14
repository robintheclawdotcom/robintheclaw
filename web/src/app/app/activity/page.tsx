"use client";

import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { ActivityList, ErrorNotice, LoadingPanel, PageHeader } from "../../../components/app-ui";
import { useAppApi } from "../../../components/app-providers";
import { LiveExecutionPanel } from "../../../components/strategy-controls";
import type { ActivityRecord } from "../../../lib/app-types";

export default function ActivityPage() {
  const api = useAppApi();
  const [items, setItems] = useState<ActivityRecord[]>([]);
  const [cursor, setCursor] = useState<string>();
  const dashboard = useQuery({ queryKey: ["dashboard"], queryFn: () => api.dashboard() });
  const isLive = dashboard.data?.agent?.mode === "live";
  const query = useQuery({
    queryKey: ["activity", cursor],
    queryFn: () => api.activity(cursor),
    enabled: Boolean(dashboard.data && !isLive),
  });
  const visible = cursor ? items.concat(query.data?.items ?? []) : query.data?.items ?? [];

  if (dashboard.isLoading || (!isLive && query.isLoading && !visible.length)) return <LoadingPanel label="Loading account activity…" />;
  if (dashboard.error || !dashboard.data || (!isLive && query.error)) return <ErrorNotice error={dashboard.error ?? query.error} retry={() => { void dashboard.refetch(); if (!isLive) void query.refetch(); }} />;
  return (
    <>
      <PageHeader eyebrow="Operations" title={isLive ? "Execution evidence" : "Activity log"} description={isLive ? "Authoritative coordinator state and venue transaction evidence." : "Chronological account, vault, and attestation events."} />
      <LiveExecutionPanel dashboard={dashboard.data} />
      {!isLive && <section className="panel activity-page"><ActivityList items={visible} />
        {query.data?.nextCursor && <button className="button button-secondary load-more" onClick={() => { setItems(visible); setCursor(query.data?.nextCursor ?? undefined); }}>Load more</button>}
      </section>}
    </>
  );
}
