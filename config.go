package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

// Config holds the persistent server configuration.
type Config struct {
	ClassicToken       string   `json:"classic_token"`
	AllowedOrgs        []string `json:"allowed_orgs,omitempty"`
	Port               int      `json:"port"`
	ValidationCacheTTL string   `json:"validation_cache_ttl"`
}

// ConfigPath returns the path to the config file.
func ConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "gh-checkproxy", "config.json")
}

// LoadConfig reads and parses the config file, applying defaults.
func LoadConfig() (*Config, error) {
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		return nil, fmt.Errorf("config not found — run 'gh-checkproxy config' to set up")
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	if cfg.Port == 0 {
		cfg.Port = 8080
	}
	if cfg.ValidationCacheTTL == "" {
		cfg.ValidationCacheTTL = "5m"
	}
	return &cfg, nil
}

// SaveConfig writes the config to disk with 0600 permissions.
func SaveConfig(cfg *Config) error {
	path := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// runConfig handles the `config` subcommand — interactive or flag-driven.
func runConfig(args []string) error {
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	classicToken := fs.String("classic-token", "", "GitHub classic personal access token")
	org := fs.String("org", "", "Restrict proxy to these organizations, comma-separated (optional)")
	port := fs.Int("port", 0, "HTTP listen port (default: 8080)")
	cacheTTL := fs.String("cache-ttl", "", "Token validation cache TTL (default: 5m)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	// Load existing config for partial updates; fall back to defaults.
	cfg, err := LoadConfig()
	if err != nil {
		cfg = &Config{Port: 8080, ValidationCacheTTL: "5m"}
	}

	reader := bufio.NewReader(os.Stdin)

	// --- Classic token ---
	if *classicToken != "" {
		cfg.ClassicToken = *classicToken
	} else {
		fmt.Print("Enter classic token (input hidden): ")
		tokenBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return fmt.Errorf("reading token: %w", err)
		}
		token := strings.TrimSpace(string(tokenBytes))
		if token == "" {
			return fmt.Errorf("classic token is required")
		}
		cfg.ClassicToken = token
	}

	// --- Organizations ---
	if *org != "" {
		cfg.AllowedOrgs = splitComma(*org)
	} else {
		fmt.Print("Fetching organizations...")
		orgs, err := fetchUserOrgs(cfg.ClassicToken)
		if err != nil {
			fmt.Fprintf(os.Stderr, " (could not fetch: %v)\n", err)
		} else {
			fmt.Println()
		}

		if len(orgs) > 0 {
			fmt.Printf("\nFound %d organization(s):\n", len(orgs))
			for i, o := range orgs {
				fmt.Printf("  %d. %s\n", i+1, o)
			}
			fmt.Print("\nSelect organizations (numbers or names, comma-separated) [leave blank for any]: ")
			line, _ := reader.ReadString('\n')
			line = strings.TrimSpace(line)
			if line != "" {
				cfg.AllowedOrgs = resolveOrgSelections(line, orgs)
			} else {
				cfg.AllowedOrgs = nil
			}
		} else {
			fmt.Print("Enter organizations to restrict to (comma-separated, leave blank for any): ")
			line, _ := reader.ReadString('\n')
			cfg.AllowedOrgs = splitComma(strings.TrimSpace(line))
		}
	}

	// --- Port ---
	if *port != 0 {
		cfg.Port = *port
	} else {
		fmt.Printf("Enter port [%d]: ", cfg.Port)
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" {
			n, err := strconv.Atoi(line)
			if err != nil || n <= 0 {
				return fmt.Errorf("invalid port: %s", line)
			}
			cfg.Port = n
		}
	}

	// --- Cache TTL ---
	if *cacheTTL != "" {
		if _, err := time.ParseDuration(*cacheTTL); err != nil {
			return fmt.Errorf("invalid cache-ttl %q: %w", *cacheTTL, err)
		}
		cfg.ValidationCacheTTL = *cacheTTL
	} else {
		fmt.Printf("Enter validation cache TTL [%s]: ", cfg.ValidationCacheTTL)
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" {
			if _, err := time.ParseDuration(line); err != nil {
				return fmt.Errorf("invalid TTL %q: %w", line, err)
			}
			cfg.ValidationCacheTTL = line
		}
	}

	if err := SaveConfig(cfg); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	fmt.Printf("\n✓ Config saved to %s\n", ConfigPath())
	if len(cfg.AllowedOrgs) > 0 {
		fmt.Printf("  Allowed orgs: %s\n", strings.Join(cfg.AllowedOrgs, ", "))
	}
	fmt.Printf("  Port: %d\n", cfg.Port)
	return nil
}

// runStatus prints the current config with the token masked.
func runStatus() error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}

	fmt.Printf("Config: %s\n\n", ConfigPath())
	fmt.Printf("  Classic token:  %s\n", maskToken(cfg.ClassicToken))
	if len(cfg.AllowedOrgs) > 0 {
		fmt.Printf("  Allowed orgs:   %s\n", strings.Join(cfg.AllowedOrgs, ", "))
	} else {
		fmt.Printf("  Allowed orgs:   (any)\n")
	}
	fmt.Printf("  Port:           %d\n", cfg.Port)
	fmt.Printf("  Cache TTL:      %s\n", cfg.ValidationCacheTTL)
	return nil
}

func maskToken(token string) string {
	if len(token) < 8 {
		return "***"
	}
	return token[:4] + "***..." + token[len(token)-4:]
}

type githubOrg struct {
	Login string `json:"login"`
}

// fetchUserOrgs lists the organizations the classic token has access to.
func fetchUserOrgs(token string) ([]string, error) {
	req, err := http.NewRequest("GET", "https://api.github.com/user/orgs?per_page=100", nil)
	if err != nil {
		return nil, err
	}
	setGitHubHeaders(req, token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var orgs []githubOrg
	if err := json.NewDecoder(resp.Body).Decode(&orgs); err != nil {
		return nil, err
	}

	names := make([]string, len(orgs))
	for i, o := range orgs {
		names[i] = o.Login
	}
	return names, nil
}

// splitComma splits a comma-separated string into trimmed, non-empty tokens.
func splitComma(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// resolveOrgSelections parses user input (numbers and/or names) against the
// fetched org list, returning the resolved org names.
func resolveOrgSelections(input string, orgs []string) []string {
	var result []string
	seen := make(map[string]struct{})
	for _, token := range splitComma(input) {
		var name string
		if n, err := strconv.Atoi(token); err == nil && n >= 1 && n <= len(orgs) {
			name = orgs[n-1]
		} else {
			name = token
		}
		if _, exists := seen[name]; !exists {
			seen[name] = struct{}{}
			result = append(result, name)
		}
	}
	return result
}

// setGitHubHeaders adds standard GitHub API headers to a request.
func setGitHubHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
}
