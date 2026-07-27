# Bare-metal deployment (without Docker)

Runs the TokenHub backend and admin console directly on a host, with no Docker
anywhere. The application itself is unchanged: the backend is a single Go binary
and the console is the Next.js standalone server.

Two entry points, for two different jobs:

| | Use it for | Needs root? | Survives reboot? |
| --- | --- | --- | --- |
| `run-local.sh` | Running the production build on your own machine | No | No |
| `build.sh` + `install.sh` | Installing on a server under systemd | Yes | Yes |

SQLite only. The backend also speaks PostgreSQL, but these scripts do not manage
it — see [Database](#database) below.

## Run locally

```bash
./deploy/bare-metal/run-local.sh          # foreground, Ctrl-C stops both
./deploy/bare-metal/run-local.sh -d       # background, returns immediately
./deploy/bare-metal/run-local.sh status
./deploy/bare-metal/run-local.sh logs -f
./deploy/bare-metal/run-local.sh stop
./deploy/bare-metal/run-local.sh restart -d
```

Builds both components if needed, then runs them on loopback. Everything lands in
`.tokenhub/` inside the repository (gitignored): the binary, the console bundle,
the database, the logs and the pid files. Deleting `.tokenhub/` removes every
trace.

In background mode the services are detached from the launching shell and survive
it, so they keep running after the terminal closes — but not across a reboot.
Both modes record pid files, so `status` and `stop` work on a foreground instance
too. Both ports are claimed up front: if either is already in use the script
refuses to start rather than reporting success against whatever is answering.

Verified on Linux. macOS has no `setsid`, so the script falls back to stopping
the process tree by walking children; that path is implemented but has not been
exercised on a macOS host.

Unlike `start.sh`, this runs the **production** build — the same standalone
bundle a deployment runs — rather than a dev server, so it surfaces problems that
only appear in a production build.

Options: `--rebuild`, `--reset` (drop the local database), `--backend-port N`,
`--console-port N`.

The console always logs to `.tokenhub/logs/console.log` (Next's startup output is
noisy enough that interleaving it with the backend's would be unreadable); the
backend logs to the terminal in the foreground and to
`.tokenhub/logs/backend.log` in the background.

## Deploy to a server

```bash
# On a build host with Go, Node 22+, npm and a C compiler:
./deploy/bare-metal/build.sh

# Copy dist/tokenhub-<version>-<os>-<arch>/ to the target host, then:
sudo ./install.sh --generate-secrets
```

Full documentation, including the upgrade, rollback and backup procedures:
[docs/deployment.md](../../docs/deployment.md#bare-metal-deployment-without-docker)
(also in [简体中文](../../docs/zh-CN/deployment.md#裸机部署不使用-docker) and
[日本語](../../docs/ja/deployment.md#ベアメタルデプロイdocker-なし)).

## Database

These scripts manage **SQLite** deployments. The database lives in the state
directory, the backup timer uses the backend's own online-backup API, and an
upgrade takes a verified snapshot first.

The backend can also run on PostgreSQL, but the installer rejects such a
configuration rather than half-supporting it: doing it properly means owning the
preflight checks, the pre-upgrade `pg_dump`, backup retention and a client
toolchain whose version has to track the server's. To run on PostgreSQL, deploy
the backend yourself and see [docs/postgresql-setup.md](../../docs/postgresql-setup.md).

## Contents

| File | Purpose |
| --- | --- |
| `run-local.sh` | Run the production build locally, no root, no systemd |
| `build.sh` | Build a self-contained release into `dist/` |
| `install.sh` | Install or upgrade on the target host |
| `uninstall.sh` | Remove the installation, keeping data unless `--purge` |
| `standalone-bundle.sh` | Shared helper that assembles the Next.js bundle |
| `backup-sqlite.mjs` | SQLite backup and retention, driven by `tokenhub-backup.timer` |
| `parse-env-file.mjs` | Reads systemd EnvironmentFiles the way systemd does |
| `backend.env.example` | Backend configuration template (systemd EnvironmentFile) |
| `frontend.env.example` | Console configuration template |
| `systemd/` | Service and timer units |

`build.sh` packages the installer, the units, the configuration examples and the
runtime scripts, so the release directory is all the target host needs.
`run-local.sh` and `standalone-bundle.sh` are build-time helpers and are
deliberately not part of a release.
