#!/bin/sh
set -eu

: "${DATABASE_URL:?DATABASE_URL is required}"

# Default mode applies pending migrations, same as always. "check" mode
# (used by the app chart's pre-upgrade hook, see
# charts/billpiggy/templates/migrations-check-job.yaml) only reports pending
# migrations and exits non-zero instead of applying them, so a deploy can be
# gated on the "Apply database migrations" workflow having already run.
mode="${1:-apply}"

psql "$DATABASE_URL" -v ON_ERROR_STOP=1 <<'SQL'
CREATE TABLE IF NOT EXISTS public.schema_migrations (
  version TEXT PRIMARY KEY,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
SQL

pending=""
for file in $(find /migrations -maxdepth 1 -name '*.up.sql' -type f | sort); do
  version=$(basename "$file" .up.sql)
  applied=$(psql "$DATABASE_URL" -tAc "SELECT 1 FROM public.schema_migrations WHERE version = '$version'")
  if [ "$applied" = "1" ]; then
    continue
  fi
  if [ "$mode" = "check" ]; then
    pending="$pending $version"
    continue
  fi
  echo "applying migration $version"
  psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f "$file"
  psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -c "INSERT INTO public.schema_migrations(version) VALUES ('$version')"
done

if [ "$mode" = "check" ] && [ -n "$pending" ]; then
  echo "pending migrations not applied:$pending" >&2
  echo "run the 'Apply database migrations' workflow for this image before deploying it" >&2
  exit 1
fi
