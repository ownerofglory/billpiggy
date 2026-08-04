# Backup and disaster recovery

This describes what the [Terraform infrastructure root](../infra/terraform) backs up
automatically, how to restore from it, and the current limitations of that setup.

## What's backed up

Two Kubernetes `CronJob`s run in the `billpiggy-infra` namespace (see
[`main.tf`](../infra/terraform/main.tf)), writing into a dedicated `billpiggy-backups`
MinIO bucket that only a scoped backup user can reach — neither the application nor
its regular MinIO user has access to it:

- **`billpiggy-postgres-backup`** (`postgres_backup_schedule`, default `0 3 * * *`)
  runs `pg_dump` against the in-cluster PostgreSQL, gzips the output, and uploads it to
  `billpiggy-backups/postgres/billpiggy-<UTC timestamp>.sql.gz`. It then deletes dumps
  older than `backup_retention_days` (default 14) from that prefix.
- **`billpiggy-minio-backup-mirror`** (`minio_backup_schedule`, default `30 3 * * *`)
  runs `mc mirror --overwrite --remove` from the live `billpiggy` bucket (receipts,
  generated reports) into `billpiggy-backups/mirror`. This is a point-in-time mirror,
  not a version history — a file deleted from `billpiggy` is also removed from the
  mirror at the next run, so it protects against bucket/cluster loss, not against
  accidental deletion in the source bucket.

## Restoring PostgreSQL

1. Scale the application down first (`kubectl scale deployment/billpiggy --replicas=0
   -n <namespace>`) so nothing writes to the database mid-restore.
2. From a pod or port-forward with network access to MinIO and `mc` installed, list
   and fetch the dump you want:
   ```bash
   mc alias set backup http://billpiggy-minio.billpiggy-infra.svc.cluster.local:9000 \
     "$MINIO_BACKUP_USER" "$MINIO_BACKUP_PASSWORD"
   mc ls backup/billpiggy-backups/postgres/
   mc cp backup/billpiggy-backups/postgres/billpiggy-<timestamp>.sql.gz .
   ```
3. Restore into the target database (a fresh one, or the existing one after dropping
   and recreating the schema — decide based on why you're restoring):
   ```bash
   gunzip -c billpiggy-<timestamp>.sql.gz | psql "$DATABASE_URL" -v ON_ERROR_STOP=1
   ```
4. Re-run the **Apply database migrations** workflow for the image tag currently
   deployed, so `public.schema_migrations` reflects reality even if the dump predates
   a later migration. It's idempotent — already-applied migrations are skipped.
5. Scale the application back up. The chart's `migrations-check` hook
   (see [production deployment](production-deployment.md#database-migrations)) will
   refuse to start if step 4 was skipped and a migration is genuinely missing.
6. Run the [post-deploy smoke workflow](../.github/workflows/post-deploy-smoke.yaml)
   against the environment to confirm `/livez`, `/readyz`, and `/startupz` are healthy.

## Restoring MinIO objects

Most of the time you want individual objects back (e.g. a receipt or report someone
deleted), not the whole bucket:

```bash
mc alias set backup http://billpiggy-minio.billpiggy-infra.svc.cluster.local:9000 \
  "$MINIO_BACKUP_USER" "$MINIO_BACKUP_PASSWORD"
mc cp backup/billpiggy-backups/mirror/<key> backup/billpiggy/<key>
```

To restore the entire bucket (e.g. after the primary MinIO volume was lost and
recreated empty), mirror in the opposite direction:

```bash
mc mirror --overwrite backup/billpiggy-backups/mirror backup/billpiggy
```

Because the mirror is overwrite-based with no history, this only helps if the loss
happened *before* the next scheduled mirror run overwrote the backup with the same
(now-missing) state — see limitations below.

## Limitations and what full DR would still need

- **Backups live in the same cluster.** `billpiggy-backups` is a bucket in the same
  MinIO release, backed by the same `local-path` persistent volumes as the primary
  data. A node failure or storage-class issue that takes out the primary bucket or
  database can take out the backups too. True disaster recovery needs these dumps and
  mirrors replicated to a destination outside the cluster (a remote object store, a
  different cluster, or an offsite `mc mirror` target) on a schedule — this isn't
  automated yet and needs an actual off-cluster destination to point at, which is an
  operational decision rather than something to fabricate here.
- **No point-in-time recovery for PostgreSQL.** Backups are daily `pg_dump` snapshots,
  so recovery is to the last completed dump, not to an arbitrary point in time. If
  that gap matters, look at continuous WAL archiving instead of (or alongside) the
  nightly dump.
- **The MinIO mirror has no version history.** It reflects the live bucket as of the
  last successful run; anything deleted from `billpiggy` before the next mirror run
  is gone from the mirror too. Enabling bucket versioning on `billpiggy-backups`
  (not currently configured) would preserve prior versions of mirrored objects
  instead of just the latest.
- **Restores are manual.** There's no automated restore workflow — deliberately, since
  a restore is a judgment call (which dump, which target, whether to take the app
  down first) that shouldn't be a one-click action in CI.
