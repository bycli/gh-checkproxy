---
name: gh-checkproxy
description: Check GitHub CI status for pull requests using gh-checkproxy, a proxy that enables Checks API access via fine-grained personal access tokens. Use when asked to check, watch, or wait for CI/checks on a PR in a repo where the Checks API is accessed through a proxy (GH_CHECKPROXY_URL is set).
---

# gh-checkproxy

Fine-grained PATs cannot call the GitHub Checks API directly. `gh-checkproxy` bridges this by proxying through a server that holds a classic token.

## Required environment variables

- `GH_CHECKPROXY_URL` — proxy server base URL
- `GH_TOKEN` — fine-grained PAT (needs **Metadata: read** on the target repo)

## Commands

```bash
# Snapshot current check status
gh-checkproxy pr checks <PR> --repo <owner>/<repo>

# Block until all checks complete
gh-checkproxy pr checks <PR> --repo <owner>/<repo> --watch

# Exit immediately on first failure
gh-checkproxy pr checks <PR> --repo <owner>/<repo> --watch --fail-fast
```

## Exit codes

| Code | Meaning |
|---|---|
| `0` | All checks passed |
| `1` | One or more checks failed |
| `8` | Checks still pending |

## Typical agent workflow

```bash
# After pushing and opening/updating a PR:
gh-checkproxy pr checks $PR --repo myorg/myrepo --watch
# exit 0 → safe to proceed
# exit 1 → investigate failures before continuing
# exit 8 → checks timed out, still pending
```
