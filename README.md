# BillPiggy

See the [production deployment guide](docs/production-deployment.md) for the GitHub
Environment secrets and variables, database migration prerequisite, and manual
deployment workflow.

## Local PostgreSQL

Start a disposable local database with tracked migrations applied on first startup:

```sh
docker compose up --build postgres
```

The default local connection string is in [.env.example](.env.example). To recreate
the database from migrations, stop Compose and remove the `billpiggy-postgres-data`
volume before starting it again.
