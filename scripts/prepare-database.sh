#!/usr/bin/env bash
set -euo pipefail

kind="${1:-}"
database_url="${2:-}"
role="${3:-}"

if [[ -z "$database_url" ]]; then
  echo "database owner URL is required" >&2
  exit 1
fi

case "$kind" in
  app)
    bash scripts/migrate-app.sh "$database_url"
    namespace="robin_app"
    ;;
  research)
    bash scripts/migrate-research.sh "$database_url"
    namespace="robin_research"
    ;;
  execution)
    bash scripts/migrate-execution.sh "$database_url"
    namespace="robin_execution"
    ;;
  lighter)
    bash scripts/migrate-lighter-provisioner.sh "$database_url"
    namespace="robin_lighter"
    ;;
  custody)
    bash scripts/migrate-robinhood-provisioner.sh "$database_url"
    bash scripts/migrate-robinhood-signer.sh "$database_url"
    ROBIN_DATABASE_URL="$database_url" ruby scripts/psql-with-url.rb --set ON_ERROR_STOP=1 <<'SQL'
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM robinhood_signer_transactions
        WHERE status IN ('signed', 'submitted', 'soft_confirmed', 'l1_posted', 'ambiguous', 'replaced')
    ) THEN
        RAISE EXCEPTION 'Robinhood signer sends must be terminal before migration release';
    END IF;
END;
$$;
SQL
    namespace="robin_custody"
    ;;
  *)
    echo "database kind must be app, research, execution, lighter, or custody" >&2
    exit 1
    ;;
esac

if [[ "$role" != "$namespace"* ]]; then
  echo "database role does not match database kind" >&2
  exit 1
fi

bash scripts/provision-database-roles.sh "$database_url" "$role"
