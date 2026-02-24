# gh-checkproxy

GitHub fine-grained personal access tokens cannot call the [Checks API](https://docs.github.com/en/rest/checks). `gh-checkproxy` bridges this gap with a lightweight proxy that holds a classic token server-side, validates incoming fine-grained tokens, and proxies only the allowed check-related endpoints.

The same binary serves two roles:

- **Server** — runs on a trusted host, holds the classic PAT
- **Client** — runs on your machine (or an AI agent's), uses a fine-grained PAT

## Install

### Homebrew (macOS, Linux)

```bash
brew tap bycli/tap
brew install gh-checkproxy
```

### apt (Ubuntu, Debian)

```bash
curl -fsSL https://bycli.github.io/gh-checkproxy/gpg.key | sudo gpg --dearmor -o /usr/share/keyrings/gh-checkproxy.gpg
echo "deb [signed-by=/usr/share/keyrings/gh-checkproxy.gpg] https://bycli.github.io/gh-checkproxy stable main" | sudo tee /etc/apt/sources.list.d/gh-checkproxy.list
sudo apt-get update
sudo apt-get install gh-checkproxy
```

### From source

```bash
go install github.com/bycli/gh-checkproxy@latest
```

### Binary releases

Download from [GitHub Releases](https://github.com/bycli/gh-checkproxy/releases).

## How it works

```
Agent (fine-grained PAT)
  → GET proxy/repos/{owner}/{repo}/commits/{sha}/check-runs
  → Proxy validates fine-grained token against GitHub (GET /repos/{owner}/{repo})
      → 403/404: reject
      → 200: re-issue request with classic token → return response
```

The classic token is never exposed to clients. The proxy only allows GET requests to a strict whitelist of Checks and Statuses API endpoints.

## Server setup

### 1. Configure

```bash
gh-checkproxy config
```

Interactive prompts will ask for:
- **Classic token** — a [classic PAT](https://github.com/settings/tokens) with `repo` scope (input is masked), or use an env var to avoid storage
- **Organizations** — optionally restrict the proxy to specific orgs (fetched from your token)
- **Port** — HTTP listen port (default: 8080)
- **Cache TTL** — how long to cache token validation results (default: 5m)

**Avoid storing the token:** Set `GH_CHECKPROXY_CLASSIC_TOKEN` or `GH_TOKEN` (classic prefix `ghp_`/`gho_`) before running `config`. The token is read from the env at runtime and never written to disk. Ensure the env var is set when running `gh-checkproxy serve`.

For scripted/CI setup:

```bash
export GH_CHECKPROXY_CLASSIC_TOKEN=ghp_xxx
gh-checkproxy config --org myorg --port 8080
```

> **Never pass tokens as CLI arguments** — they are visible in `ps`, `/proc`, and shell history.

Config is saved to `~/.config/gh-checkproxy/config.json` (permissions `0600`).

### 2. Start the server

```bash
gh-checkproxy serve
```

> **Note:** The server listens on plain HTTP. Run it on `localhost` or behind a TLS-terminating reverse proxy for production use.

### 3. Check status

```bash
gh-checkproxy status
```

## Client usage

### Environment variables

```bash
export GH_CHECKPROXY_URL=http://localhost:8080   # proxy base URL
export GH_TOKEN=github_pat_xxx                    # fine-grained PAT (needs Metadata: read)
```

### Check PR status

```bash
# By PR number
gh-checkproxy pr checks 42 --repo myorg/myrepo

# By branch name
gh-checkproxy pr checks feature-branch --repo myorg/myrepo

# Auto-detect repo and branch from git
gh-checkproxy pr checks

# Watch until all checks complete
gh-checkproxy pr checks 42 --repo myorg/myrepo --watch

# Exit immediately on first failure
gh-checkproxy pr checks 42 --repo myorg/myrepo --watch --fail-fast
```

### Exit codes

| Code | Meaning |
|------|---------|
| `0`  | All checks passed |
| `1`  | One or more checks failed |
| `8`  | Checks still pending |

These match `gh pr checks` conventions.

## Allowed proxy endpoints

All GET-only:

| Category | Endpoints |
|----------|-----------|
| Check Runs | `/repos/{owner}/{repo}/commits/{ref}/check-runs` |
| | `/repos/{owner}/{repo}/check-runs/{id}` |
| | `/repos/{owner}/{repo}/check-runs/{id}/annotations` |
| Check Suites | `/repos/{owner}/{repo}/commits/{ref}/check-suites` |
| | `/repos/{owner}/{repo}/check-suites/{id}` |
| | `/repos/{owner}/{repo}/check-suites/{id}/check-runs` |
| Statuses | `/repos/{owner}/{repo}/commits/{ref}/status` |
| | `/repos/{owner}/{repo}/commits/{ref}/statuses` |
| | `/repos/{owner}/{repo}/statuses/{sha}` |

All other paths return 404. Non-GET methods return 405.

## Security model

- The **classic token** stays on the server — never sent to clients
- **Fine-grained tokens** are validated by calling `GET /repos/{owner}/{repo}` on GitHub — a 200 means the token has read access to that repo
- Validation results are cached in memory (keyed by `SHA-256(token/owner/repo)`) with a configurable TTL
- The proxy only forwards to `api.github.com` — no SSRF vectors
- Organization restrictions limit which repos can be accessed through the proxy
- Config file is stored with `0600` permissions (owner-only read/write) — verify the host is trusted
- **The server listens on plain HTTP** — run on `localhost` or behind a TLS reverse proxy (nginx, Caddy) to protect tokens in transit

## Token requirements

| Token | Type | Scopes |
|-------|------|--------|
| Server (classic) | [Classic PAT](https://github.com/settings/tokens) | `repo` |
| Client (fine-grained) | [Fine-grained PAT](https://github.com/settings/personal-access-tokens) | Metadata: read on target repos |

## License

MIT
