package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type cacheEntry struct {
	allowed bool
	expires time.Time
}

// Validator checks whether a fine-grained token has read access to a repository.
// Results are cached in memory to avoid repeated GitHub API calls.
type Validator struct {
	cache      sync.Map
	ttl        time.Duration
	httpClient *http.Client
}

func NewValidator(ttl time.Duration) *Validator {
	return &Validator{
		ttl:        ttl,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (v *Validator) cacheKey(token, owner, repo string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s/%s/%s", token, owner, repo)
	return hex.EncodeToString(h.Sum(nil))
}

// Validate returns true if the fine-grained token can read the given repository.
func (v *Validator) Validate(ctx context.Context, token, owner, repo string) (bool, error) {
	key := v.cacheKey(token, owner, repo)

	if val, ok := v.cache.Load(key); ok {
		entry := val.(cacheEntry)
		if time.Now().Before(entry.expires) {
			return entry.allowed, nil
		}
		v.cache.Delete(key)
	}

	allowed, err := v.checkGitHub(ctx, token, owner, repo)
	if err != nil {
		return false, err
	}

	v.cache.Store(key, cacheEntry{
		allowed: allowed,
		expires: time.Now().Add(v.ttl),
	})
	return allowed, nil
}

func (v *Validator) checkGitHub(ctx context.Context, token, owner, repo string) (bool, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repo)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false, err
	}
	setGitHubHeaders(req, token)

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("validating token against GitHub: %w", err)
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK, nil
}
