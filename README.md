# BillPiggy

## Local PostgreSQL

Start a disposable local database with tracked migrations applied on first startup:

```sh
docker compose up --build postgres
```

The default local connection string is in [.env.example](.env.example). To recreate
the database from migrations, stop Compose and remove the `billpiggy-postgres-data`
volume before starting it again.
