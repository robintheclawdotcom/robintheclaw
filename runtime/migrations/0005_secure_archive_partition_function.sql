CREATE OR REPLACE FUNCTION public.ensure_event_staging_partition(event_time TIMESTAMPTZ)
RETURNS VOID
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    month_start TIMESTAMPTZ :=
        date_trunc('month', event_time AT TIME ZONE 'UTC') AT TIME ZONE 'UTC';
    month_end TIMESTAMPTZ := month_start + INTERVAL '1 month';
    partition_name TEXT := format(
        'event_staging_y%sm%s',
        to_char(month_start AT TIME ZONE 'UTC', 'YYYY'),
        to_char(month_start AT TIME ZONE 'UTC', 'MM')
    );
BEGIN
    IF NOT isfinite(event_time)
       OR event_time < clock_timestamp() - INTERVAL '62 days'
       OR event_time > clock_timestamp() + INTERVAL '1 day' THEN
        RAISE EXCEPTION 'staging partition time is outside the capture window';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtext(partition_name));
    EXECUTE format(
        'CREATE TABLE IF NOT EXISTS public.%I PARTITION OF public.event_staging FOR VALUES FROM (%L) TO (%L)',
        partition_name,
        month_start,
        month_end
    );
END;
$$;

REVOKE ALL ON FUNCTION public.ensure_event_staging_partition(TIMESTAMPTZ) FROM PUBLIC;
