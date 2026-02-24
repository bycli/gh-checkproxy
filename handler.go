package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// allowedRoutes is the whitelist of permitted API paths. All are GET-only.
var allowedRoutes = []*regexp.Regexp{
	// Checks API
	regexp.MustCompile(`^/repos/[^/]+/[^/]+/commits/[^/]+/check-runs$`),
	regexp.MustCompile(`^/repos/[^/]+/[^/]+/commits/[^/]+/check-suites$`),
	regexp.MustCompile(`^/repos/[^/]+/[^/]+/check-runs/[^/]+$`),
	regexp.MustCompile(`^/repos/[^/]+/[^/]+/check-runs/[^/]+/annotations$`),
	regexp.MustCompile(`^/repos/[^/]+/[^/]+/check-suites/[^/]+$`),
	regexp.MustCompile(`^/repos/[^/]+/[^/]+/check-suites/[^/]+/check-runs$`),
	// Commit Statuses API
	regexp.MustCompile(`^/repos/[^/]+/[^/]+/commits/[^/]+/status$`),
	regexp.MustCompile(`^/repos/[^/]+/[^/]+/commits/[^/]+/statuses$`),
	regexp.MustCompile(`^/repos/[^/]+/[^/]+/statuses/[^/]+$`),
}

const githubAPIBase = "https://api.github.com"

// headersToForward are the upstream response headers passed through to the client.
var headersToForward = []string{
	"Content-Type",
	"ETag",
	"Link",
	"X-RateLimit-Limit",
	"X-RateLimit-Remaining",
	"X-RateLimit-Reset",
	"X-RateLimit-Used",
	"X-RateLimit-Resource",
}

func pathMatches(path string) bool {
	for _, re := range allowedRoutes {
		if re.MatchString(path) {
			return true
		}
	}
	return false
}

// extractOwnerRepo parses /repos/{owner}/{repo}/... and returns owner and repo.
func extractOwnerRepo(path string) (owner, repo string, ok bool) {
	parts := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 4)
	if len(parts) < 3 || parts[0] != "repos" {
		return "", "", false
	}
	return parts[1], parts[2], true
}

// ProxyHandler returns an http.HandlerFunc that:
//  1. Validates the fine-grained token has access to the requested repo
//  2. Proxies allowed GET requests to GitHub using the classic token
func ProxyHandler(cfg *Config, validator *Validator) http.HandlerFunc {
	upstreamClient := &http.Client{Timeout: 30 * time.Second}

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		path := r.URL.Path
		if !pathMatches(path) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		owner, repo, ok := extractOwnerRepo(path)
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		if len(cfg.AllowedOrgs) > 0 && !orgAllowed(cfg.AllowedOrgs, owner) {
			http.Error(w, "forbidden: organization not allowed", http.StatusForbidden)
			return
		}

		fgToken := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if fgToken == "" {
			http.Error(w, "unauthorized: missing Authorization header", http.StatusUnauthorized)
			return
		}

		allowed, err := validator.Validate(r.Context(), fgToken, owner, repo)
		if err != nil {
			http.Error(w, fmt.Sprintf("error validating token: %v", err), http.StatusInternalServerError)
			return
		}
		if !allowed {
			http.Error(w, "forbidden: token does not have access to this repository", http.StatusForbidden)
			return
		}

		upstreamURL := githubAPIBase + path
		if r.URL.RawQuery != "" {
			upstreamURL += "?" + r.URL.RawQuery
		}

		upstreamReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, upstreamURL, nil)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		setGitHubHeaders(upstreamReq, cfg.ClassicToken)

		upstreamResp, err := upstreamClient.Do(upstreamReq)
		if err != nil {
			http.Error(w, fmt.Sprintf("upstream error: %v", err), http.StatusBadGateway)
			return
		}
		defer upstreamResp.Body.Close()

		for _, h := range headersToForward {
			if val := upstreamResp.Header.Get(h); val != "" {
				w.Header().Set(h, val)
			}
		}
		w.WriteHeader(upstreamResp.StatusCode)
		_, _ = io.Copy(w, upstreamResp.Body)
	}
}

// orgAllowed reports whether owner is in the allowed orgs list (case-insensitive).
func orgAllowed(allowedOrgs []string, owner string) bool {
	for _, o := range allowedOrgs {
		if strings.EqualFold(o, owner) {
			return true
		}
	}
	return false
}

// runServe loads config and starts the HTTP proxy server.
func runServe() {
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n\nRun 'gh-checkproxy config' to set up.\n", err)
		os.Exit(1)
	}

	ttl, err := time.ParseDuration(cfg.ValidationCacheTTL)
	if err != nil {
		ttl = 5 * time.Minute
	}

	validator := NewValidator(ttl)
	mux := http.NewServeMux()
	mux.HandleFunc("/", ProxyHandler(cfg, validator))

	addr := fmt.Sprintf(":%d", cfg.Port)
	fmt.Printf("gh-checkproxy listening on %s\n", addr)
	if len(cfg.AllowedOrgs) > 0 {
		fmt.Printf("  Restricting to orgs: %s\n", strings.Join(cfg.AllowedOrgs, ", "))
	} else {
		fmt.Printf("  Allowed orgs: (any — set --org to restrict)\n")
	}
	fmt.Printf("  Allowed routes: %d\n", len(allowedRoutes))
	fmt.Printf("  Cache TTL: %s\n\n", cfg.ValidationCacheTTL)

	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}
