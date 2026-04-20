# AGENTS.md — VictoriaMetrics (sufficit fork)

## Build and File Conventions

- **Build outputs**: always place compiled artifacts inside `bin/` (relative to project root). Use `make victoria-metrics` or `go build` with `-o bin/` as output target.
- **Temporary files**: all temporary files created during agent work (scripts, dumps, test data, audit outputs) must go inside `tmp/` (relative to project root). Do not create temporary files in the project root.

## Repository Info

- **Fork**: `sufficit/VictoriaMetrics`
- **Upstream**: `VictoriaMetrics/VictoriaMetrics`
- **Branch**: `master`

## Build Commands

```bash
# Build single-node binary
make victoria-metrics

# Or directly with Go
go build -o bin/victoria-metrics ./app/victoria-metrics/
```

## Sufficit Customizations

Changes relative to upstream are minimal and focused on:

- Retention filter support and duration parsing (`ms` suffix)
- Retention cleanup scripts: `_test_delete_end.py`

## Notes

- Merge upstream changes with `git fetch upstream ; git merge upstream/master`
- Never commit secrets, passwords, or TLS keys to this repository
- Validation scripts (`_validate_deploy.sh`, `_validate_deploy2.sh`) are local helpers, not part of the upstream build
