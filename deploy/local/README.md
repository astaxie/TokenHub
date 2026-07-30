# Local production-build runner (without Docker)

Runs the TokenHub backend and admin console on your own machine from a
production build, with no Docker, no root and no systemd. The application itself
is unchanged: the backend is a single Go binary and the console is the Next.js
standalone server.

This is **not** a deployment path. To install TokenHub on a server, use the
native systemd installer described in
[docs/deployment.md](../../docs/deployment.md#native-release-with-systemd), or
Docker Compose.

```bash
./deploy/local/run-local.sh          # foreground, Ctrl-C stops both
./deploy/local/run-local.sh -d       # background, returns immediately
./deploy/local/run-local.sh status
./deploy/local/run-local.sh logs -f
./deploy/local/run-local.sh stop
./deploy/local/run-local.sh restart -d
```

Builds both components if needed, then runs them on loopback. All runtime state
lands in `.tokenhub/` inside the repository (gitignored): the binary, the console
bundle, the database, the logs and the pid files. Deleting `.tokenhub/` resets
the instance; building may also refresh the usual ignored frontend artefacts
(`frontend/node_modules`, `frontend/.next`). Nothing is installed system-wide and
no service account is created.

In background mode the services are detached from the launching shell and survive
it, so they keep running after the terminal closes — but not across a reboot.
Both modes record pid files, so `status` and `stop` work on a foreground instance
too. `stop` verifies that the recorded pid still belongs to this instance before
signalling it, so a recycled pid is never killed by mistake. Both ports are
claimed up front: if either is already in use the script refuses to start rather
than reporting success against whatever is answering.

Verified on Linux. macOS has no `setsid`, so the script falls back to stopping
the process tree by walking children; that path is implemented but has not been
exercised on a macOS host.

Unlike `start.sh`, this runs the **production** build — the same standalone
bundle a deployment runs — rather than a dev server, so it surfaces problems that
only appear in a production build. It uses development credentials
(`admin` / `admin123456`) and binds loopback only.

SQLite only, at `.tokenhub/tokenhub.db`. Running locally against PostgreSQL is
out of scope for this script; see
[docs/postgresql-setup.md](../../docs/postgresql-setup.md).

Options: `--rebuild`, `--reset` (drop the local database), `--backend-port N`,
`--console-port N`.

The console always logs to `.tokenhub/logs/console.log` (Next's startup output is
noisy enough that interleaving it with the backend's would be unreadable); the
backend logs to the terminal in the foreground and to
`.tokenhub/logs/backend.log` in the background.

## Contents

| File | Purpose |
| --- | --- |
| `run-local.sh` | Run the production build locally, no root, no systemd |
| `standalone-bundle.sh` | Shared helper that assembles the Next.js bundle |

Requires Go (the version in `backend/go.mod`), Node 22 or newer, npm and a C
compiler, because the backend links SQLite through cgo.
