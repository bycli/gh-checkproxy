package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// pendingExitCode matches the gh CLI convention: exit 8 when checks are still pending.
const pendingExitCode = 8

// prInfo holds the fields we need from the GitHub PR API response.
type prInfo struct {
	Number int `json:"number"`
	Head   struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"head"`
	HeadRefName string `json:"head_ref"`
}

// GitHub REST API response types for checks and statuses.

type checkRunsResponse struct {
	TotalCount int        `json:"total_count"`
	CheckRuns  []checkRun `json:"check_runs"`
}

type checkRun struct {
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Conclusion  string    `json:"conclusion"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
	HTMLURL     string    `json:"html_url"`
	Output      struct {
		Title   string `json:"title"`
		Summary string `json:"summary"`
	} `json:"output"`
	CheckSuite struct {
		App struct {
			Slug string `json:"slug"`
		} `json:"app"`
	} `json:"check_suite"`
}

type combinedStatus struct {
	State    string         `json:"state"`
	Statuses []commitStatus `json:"statuses"`
}

type commitStatus struct {
	State       string    `json:"state"`
	Context     string    `json:"context"`
	Description string    `json:"description"`
	TargetURL   string    `json:"target_url"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// runPrChecks is the entry point for `gh-checkproxy pr checks`.
// Returns (exitCode, error). exitCode 0=pass, 1=fail, 8=pending.
func runPrChecks(args []string) (int, error) {
	fs := flag.NewFlagSet("pr checks", flag.ContinueOnError)
	repo := fs.String("repo", "", "Repository in owner/repo format (auto-detected from git remote)")
	proxyURL := fs.String("proxy-url", "", "Proxy server base URL (or $GH_CHECKPROXY_URL)")
	token := fs.String("token", "", "Fine-grained GitHub token (or $GH_TOKEN / $GITHUB_TOKEN)")
	watch := fs.Bool("watch", false, "Watch checks until they finish")
	failFast := fs.Bool("fail-fast", false, "Exit on first failure in watch mode (requires --watch)")
	interval := fs.Duration("interval", 10*time.Second, "Refresh interval in watch mode")
	_ = fs.Bool("required", false, "Only show required checks") // reserved for future use

	// parseInterspersed allows flags and positional args in any order.
	// Go's flag package stops at the first non-flag arg, so we loop: parse
	// flags, consume one positional arg, repeat.
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return 1, err
	}

	if *failFast && !*watch {
		return 1, fmt.Errorf("--fail-fast requires --watch")
	}

	// Resolve fine-grained token.
	fgToken := firstNonEmpty(*token, os.Getenv("GH_TOKEN"), os.Getenv("GITHUB_TOKEN"))
	if fgToken == "" {
		return 1, fmt.Errorf("no token: set GH_TOKEN, GITHUB_TOKEN, or use --token")
	}

	// Resolve proxy URL.
	pURL := firstNonEmpty(*proxyURL, os.Getenv("GH_CHECKPROXY_URL"))
	if pURL == "" {
		return 1, fmt.Errorf("no proxy URL: set GH_CHECKPROXY_URL or use --proxy-url")
	}
	pURL = strings.TrimRight(pURL, "/")

	// Resolve owner/repo.
	repoStr := *repo
	if repoStr == "" {
		repoStr = detectRepo()
	}
	if repoStr == "" {
		return 1, fmt.Errorf("could not detect repository: use --repo owner/repo")
	}
	repoParts := strings.SplitN(repoStr, "/", 2)
	if len(repoParts) != 2 || repoParts[0] == "" || repoParts[1] == "" {
		return 1, fmt.Errorf("invalid repo format %q: use owner/repo", repoStr)
	}
	owner, repoName := repoParts[0], repoParts[1]

	selector := ""
	if len(positional) > 0 {
		selector = positional[0]
	}

	httpClient := &http.Client{Timeout: 15 * time.Second}

	pr, err := findPR(httpClient, fgToken, owner, repoName, selector)
	if err != nil {
		return 1, fmt.Errorf("finding PR: %w", err)
	}

	tty := isTTY()
	out := os.Stdout

	checks, counts, err := fetchAndAggregateChecks(httpClient, fgToken, pURL, owner, repoName, pr.Head.SHA)
	if err != nil {
		return 1, err
	}

	if *watch {
		for {
			if tty {
				// Clear screen and move cursor to top.
				fmt.Fprint(out, "\033[2J\033[H")
				fmt.Fprintf(out, "Refreshing checks status every %.0fs. Press Ctrl+C to quit.\n\n",
					interval.Seconds())
			}

			printSummary(out, counts, tty)
			printTable(out, checks, tty)

			if counts.Pending == 0 {
				break
			}
			if *failFast && counts.Failed > 0 {
				break
			}

			time.Sleep(*interval)

			checks, counts, err = fetchAndAggregateChecks(httpClient, fgToken, pURL, owner, repoName, pr.Head.SHA)
			if err != nil {
				return 1, err
			}
		}

		// Print final result after watch ends.
		if tty {
			fmt.Fprint(out, "\033[2J\033[H")
		}
		printSummary(out, counts, tty)
		printTable(out, checks, tty)
	} else {
		printSummary(out, counts, tty)
		printTable(out, checks, tty)
	}

	if counts.Failed > 0 {
		return 1, nil
	}
	if counts.Pending > 0 {
		return pendingExitCode, nil
	}
	return 0, nil
}

// findPR resolves a PR by number, URL, branch name, or current branch.
func findPR(client *http.Client, token, owner, repo, selector string) (*prInfo, error) {
	// No selector: use the current git branch.
	if selector == "" {
		branch, err := currentBranch()
		if err != nil {
			return nil, fmt.Errorf("no PR selector provided and could not detect current branch: %w", err)
		}
		return findPRByBranch(client, token, owner, repo, branch)
	}

	// Strip leading #.
	selector = strings.TrimPrefix(selector, "#")

	// PR number.
	if n, err := strconv.Atoi(selector); err == nil {
		apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d", owner, repo, n)
		return fetchSinglePR(client, token, apiURL)
	}

	// PR URL: extract number.
	if strings.HasPrefix(selector, "https://") {
		prURLRe := regexp.MustCompile(`/pull/(\d+)`)
		if m := prURLRe.FindStringSubmatch(selector); len(m) >= 2 {
			n, _ := strconv.Atoi(m[1])
			apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d", owner, repo, n)
			return fetchSinglePR(client, token, apiURL)
		}
	}

	// Treat as branch name.
	return findPRByBranch(client, token, owner, repo, selector)
}

func findPRByBranch(client *http.Client, token, owner, repo, branch string) (*prInfo, error) {
	apiURL := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/pulls?head=%s:%s&state=open&per_page=5",
		owner, repo,
		url.QueryEscape(owner), url.QueryEscape(branch),
	)
	prs, err := fetchPRList(client, token, apiURL)
	if err != nil {
		return nil, err
	}
	if len(prs) == 0 {
		return nil, fmt.Errorf("no open pull request found for branch %q in %s/%s", branch, owner, repo)
	}
	return &prs[0], nil
}

func fetchSinglePR(client *http.Client, token, apiURL string) (*prInfo, error) {
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	setGitHubHeaders(req, token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("pull request not found (verify the PR number and that the token has Metadata: read access to the repository)")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}
	var pr prInfo
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, err
	}
	return &pr, nil
}

func fetchPRList(client *http.Client, token, apiURL string) ([]prInfo, error) {
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	setGitHubHeaders(req, token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}
	var prs []prInfo
	if err := json.NewDecoder(resp.Body).Decode(&prs); err != nil {
		return nil, err
	}
	return prs, nil
}

// fetchAndAggregateChecks retrieves check runs and commit statuses via the proxy,
// then aggregates them into the unified check slice used for display.
func fetchAndAggregateChecks(client *http.Client, token, proxyBase, owner, repo, sha string) ([]check, checkCounts, error) {
	checkRunsURL := fmt.Sprintf("%s/repos/%s/%s/commits/%s/check-runs?per_page=100",
		proxyBase, owner, repo, sha)
	runs, err := fetchCheckRuns(client, token, checkRunsURL)
	if err != nil {
		return nil, checkCounts{}, fmt.Errorf("fetching check runs: %w", err)
	}

	statusURL := fmt.Sprintf("%s/repos/%s/%s/commits/%s/status", proxyBase, owner, repo, sha)
	combined, err := fetchCombinedStatus(client, token, statusURL)
	if err != nil {
		return nil, checkCounts{}, fmt.Errorf("fetching commit status: %w", err)
	}

	var checks []check
	var counts checkCounts

	// Deduplicate check runs by name: sort by StartedAt descending, keep first seen.
	sortCheckRunsByTime(runs)
	seenRuns := make(map[string]struct{})
	for _, run := range runs {
		if _, exists := seenRuns[run.Name]; exists {
			continue
		}
		seenRuns[run.Name] = struct{}{}
		c := checkFromRun(run)
		incrementCounts(&counts, c.Bucket)
		checks = append(checks, c)
	}

	// Add commit statuses (deduplicated by context name).
	seenContexts := make(map[string]struct{})
	for _, s := range combined.Statuses {
		if _, exists := seenContexts[s.Context]; exists {
			continue
		}
		seenContexts[s.Context] = struct{}{}
		c := checkFromStatus(s)
		incrementCounts(&counts, c.Bucket)
		checks = append(checks, c)
	}

	return checks, counts, nil
}

func checkFromRun(run checkRun) check {
	state := run.Status
	if strings.EqualFold(run.Status, "completed") {
		state = run.Conclusion
	}

	c := check{
		Name:        run.Name,
		State:       strings.ToUpper(state),
		Link:        run.HTMLURL,
		StartedAt:   run.StartedAt,
		CompletedAt: run.CompletedAt,
		Description: run.Output.Title,
	}

	switch strings.ToUpper(state) {
	case "SUCCESS":
		c.Bucket = "pass"
	case "SKIPPED", "NEUTRAL":
		c.Bucket = "skipping"
	case "FAILURE", "ERROR", "TIMED_OUT", "ACTION_REQUIRED":
		c.Bucket = "fail"
	case "CANCELLED":
		c.Bucket = "cancel"
	default: // in_progress, queued, waiting, pending, requested, stale
		c.Bucket = "pending"
	}
	return c
}

func checkFromStatus(s commitStatus) check {
	c := check{
		Name:        s.Context,
		State:       strings.ToUpper(s.State),
		Link:        s.TargetURL,
		Description: s.Description,
		StartedAt:   s.CreatedAt,
		CompletedAt: s.UpdatedAt,
	}

	switch strings.ToLower(s.State) {
	case "success":
		c.Bucket = "pass"
	case "failure", "error":
		c.Bucket = "fail"
	default: // pending
		c.Bucket = "pending"
	}
	return c
}

func incrementCounts(counts *checkCounts, bucket string) {
	switch bucket {
	case "pass":
		counts.Passed++
	case "fail":
		counts.Failed++
	case "pending":
		counts.Pending++
	case "skipping":
		counts.Skipping++
	case "cancel":
		counts.Canceled++
	}
}

// fetchCheckRuns follows Link pagination to retrieve all check runs.
func fetchCheckRuns(client *http.Client, token, rawURL string) ([]checkRun, error) {
	var all []checkRun
	nextURL := rawURL
	for nextURL != "" {
		req, err := http.NewRequest(http.MethodGet, nextURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("proxy returned %d for check-runs", resp.StatusCode)
		}

		var result checkRunsResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			_ = resp.Body.Close()
			return nil, err
		}
		_ = resp.Body.Close()

		all = append(all, result.CheckRuns...)
		nextURL = parseNextLink(resp.Header.Get("Link"))
	}
	return all, nil
}

func fetchCombinedStatus(client *http.Client, token, rawURL string) (*combinedStatus, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("proxy returned %d for commit status", resp.StatusCode)
	}

	var result combinedStatus
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

// parseNextLink extracts the URL for rel="next" from a Link header.
func parseNextLink(linkHeader string) string {
	if linkHeader == "" {
		return ""
	}
	for _, part := range strings.Split(linkHeader, ",") {
		part = strings.TrimSpace(part)
		segments := strings.Split(part, ";")
		if len(segments) < 2 {
			continue
		}
		urlPart := strings.TrimSpace(segments[0])
		relPart := strings.TrimSpace(segments[1])
		if relPart == `rel="next"` && len(urlPart) > 2 {
			return urlPart[1 : len(urlPart)-1]
		}
	}
	return ""
}

// detectRepo infers owner/repo from the git remote URL.
func detectRepo() string {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return parseGitRemote(strings.TrimSpace(string(out)))
}

var gitRemoteRe = regexp.MustCompile(`github\.com[:/]([^/]+/[^/.]+?)(?:\.git)?$`)

func parseGitRemote(remote string) string {
	m := gitRemoteRe.FindStringSubmatch(remote)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

func currentBranch() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func sortCheckRunsByTime(runs []checkRun) {
	// Sort descending by StartedAt so deduplication keeps the most recent.
	for i := 0; i < len(runs); i++ {
		for j := i + 1; j < len(runs); j++ {
			if runs[j].StartedAt.After(runs[i].StartedAt) {
				runs[i], runs[j] = runs[j], runs[i]
			}
		}
	}
}

// parseInterspersed parses flags from args even when positional arguments
// appear before or between flags. Go's flag package stops at the first
// non-flag argument, so we loop: parse flags, save one positional arg, repeat.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		// fs.Args()[0] is the positional arg that stopped parsing; save it
		// and continue parsing the rest.
		positional = append(positional, args[0])
		args = args[1:]
	}
	return positional, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
