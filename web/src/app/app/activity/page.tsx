"use client";

import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { ActivityList, ErrorNotice, LoadingPanel, PageHeader } from "../../../components/app-ui";
import { useAppApi } from "../../../components/app-providers";
import type { ActivityRecord } from "../../../lib/app-types";

export default function ActivityPage() {
  const api = useAppApi();
  const [items, setItems] = useState<ActivityRecord[]>([]);
  const [cursor, setCursor] = useState<string>();
  const query = useQuery({ queryKey: ["activity", cursor], queryFn: () => api.activity(cursor) });
  const visible = cursor ? items.concat(query.data?.items ?? []) : query.data?.items ?? [];

  if (query.isLoading && !visible.length) return <LoadingPanel label="Loading account activity…" />;
  if (query.error) return <ErrorNotice error={query.error} retry={() => void query.refetch()} />;
  return (
    <>
      <PageHeader eyebrow="Operations" title="Activity log" description="Chronological account, vault, execution, and attestation events." />
      <section className="panel activity-page"><ActivityList items={visible} />
        {query.data?.nextCursor && <button className="button button-secondary load-more" onClick={() => { setItems(visible); setCursor(query.data?.nextCursor ?? undefined); }}>Load more</button>}
      </section>
    </>
  );
}
