---
name: gh-checkproxy
description: Use gh-checkproxy as a Checks API bridge when working with gh CLI or gh-based skills and only a fine-grained PAT is available. Use it to fetch/watch PR checks (check-runs and statuses) via GH_CHECKPROXY_URL, then continue the user's requested workflow with normal gh commands.
---

# gh-checkproxy

Use `gh-checkproxy` only for checks/status visibility that fine-grained tokens cannot read directly. Keep all other repository actions in regular `gh` CLI flow.

## Client usage (primary)

### Environment variables

- `GH_CHECKPROXY_URL` — proxy base URL (e.g. `http://localhost:8080`)
- `GH_TOKEN` — fine-grained PAT with **Metadata: read** on the target repo

Set via `export`, `.env`, shell profile, or CI secrets.

### Commands

```bash
gh-checkproxy pr checks <PR> --repo <owner>/<repo>           # snapshot
gh-checkproxy pr checks <PR> --repo <owner>/<repo> --watch  # block until done
gh-checkproxy pr checks <PR> --repo <owner>/<repo> --watch --fail-fast  # exit on first failure
gh-checkproxy pr checks                                      # auto-detect from git
```

### Exit codes

| Code | Meaning |
|------|---------|
| `0` | All checks passed |
| `1` | One or more failed |
| `8` | Still pending |

### Workflow

```bash
gh-checkproxy pr checks $PR --repo myorg/myrepo --watch
# exit 0 → proceed | exit 1 → investigate | exit 8 → still pending

gh pr view $PR --repo myorg/myrepo   # continue with normal gh
```

Re-run `--watch` when the user asks to recheck CI.

## Client setup

Quick verification: `gh-checkproxy pr checks 1 --repo owner/repo`. Connection error → wrong URL or server down. 403 → `GH_TOKEN` missing or lacks Metadata: read.

## Server setup (secondary)

For deploying the proxy on a trusted host (one-time or occasional setup):

### 1. Configure

```bash
gh-checkproxy config
```

Interactive prompts: classic PAT (`repo` scope), optional org restriction, port (default 8080), cache TTL (default 5m). Or use flags:

```bash
gh-checkproxy config --classic-token ghp_xxx --org myorg --port 8080
```

Config: `~/.config/gh-checkproxy/config.json`

### 2. Start the server

```bash
gh-checkproxy serve
```

Run on localhost or behind a TLS-terminating reverse proxy for production.

### 3. Check status

```bash
gh-checkproxy status
```
