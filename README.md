# Lawang Go

Lawang Go is an independent, learning-first Go/PostgreSQL implementation of the
Lawang Verification bounded context. This repository does not share migrations,
dependencies, generated files, or a database with the TypeScript project.

The current executable slice provides typed startup configuration, JSON logging,
graceful HTTP shutdown, `GET /health/live`, an isolated PostgreSQL development
service, the first forward-only migration, a raw SQL exercise/proof, and sqlc output.

## Requirements

- Go `1.25.12` (the module declares a minimum language/tool dependency level of
  `1.25.7` and pins toolchain `1.25.12`)
- Docker Engine/Desktop with Compose
- PostgreSQL `psql`
- GNU or BSD Make

Project tools are installed into the ignored `./bin` directory:

| Tool | Pinned version |
| --- | --- |
| sqlc | `v1.30.0` |
| Goose | `v3.27.1` |
| Staticcheck | `2026.1` (`v0.7.0`) |

sqlc `v1.31.x` is intentionally not used because it requires Go 1.26, outside this
project's approved Go 1.25 baseline.

## First setup

```sh
cp .env.example .env
make tools
make deps-up
make migrate
```

The development database is `lawang_db_go`. Integration tests never use it; they
start and terminate their own PostgreSQL container.

Goose is configured through the environment contract in `.env.example` and explicit
Makefile arguments. The current Goose CLI does not consume a `goose.yaml` file, so an
inert YAML file is deliberately not included.

## Exercise the SQL path

Run the human-readable insert/select exercise:

```sh
make sql
```

Run the assertion-style proof for database-generated UUID, default `created` state,
timestamps, and the bounded status constraint:

```sh
make proof
```

Both commands run committed SQL directly through `psql`.

## Run the API

```sh
make run
```

In another terminal:

```sh
curl -i http://localhost:8080/health/live
```

Expected body:

```json
{"status":"ok"}
```

Stop the process with `Ctrl-C`; SIGINT and SIGTERM trigger bounded graceful shutdown.

## Quality commands

```sh
make fmt-check
make vet
make staticcheck
make test
make sqlc-diff
make migration-validate
make compose-validate
```

`make test` includes the real PostgreSQL integration test and therefore requires a
working Docker daemon. It does not silently skip that test when Docker is missing.
Run all current checks with:

```sh
make quality
```

Generated code under `internal/adapter/postgres/sqlc` is committed output. Change the
handwritten migration/query files and regenerate with `make sqlc-generate`; never edit
generated files directly.

## Stop development dependencies

```sh
make deps-down
```

This stops containers without deleting the named development volume.
