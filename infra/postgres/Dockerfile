FROM postgres:16-alpine

COPY migrations/*.up.sql /docker-entrypoint-initdb.d/

HEALTHCHECK --interval=5s --timeout=3s --start-period=5s --retries=10 \
  CMD pg_isready --username="$${POSTGRES_USER}" --dbname="$${POSTGRES_DB}"
