#!/bin/sh
set -eu

: "${DATABASE_URL:?DATABASE_URL is required}"

psql "$DATABASE_URL" -v ON_ERROR_STOP=1 <<'SQL'
CREATE TABLE IF NOT EXISTS public.schema_migrations (
  version TEXT PRIMARY KEY,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
SQL

for file in $(find /migrations -maxdepth 1 -name '*.up.sql' -type f | sort); do
  version=$(basename "$file" .up.sql)
  applied=$(psql "$DATABASE_URL" -tAc "SELECT 1 FROM public.schema_migrations WHERE version = '$version'")
  if [ "$applied" = "1" ]; then
    continue
  fi
  echo "applying migration $version"
  psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f "$file"
  psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -c "INSERT INTO public.schema_migrations(version) VALUES ('$version')"
done
