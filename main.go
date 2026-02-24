package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		runServe()
		return
	}

	switch os.Args[1] {
	case "config":
		if err := runConfig(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "serve":
		runServe()
	case "status":
		if err := runStatus(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "pr":
		if len(os.Args) < 3 || os.Args[2] != "checks" {
			fmt.Fprintln(os.Stderr, "usage: gh-checkproxy pr checks [<number>|<url>|<branch>] [flags]")
			os.Exit(1)
		}
		code, err := runPrChecks(os.Args[3:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			if code == 0 {
				code = 1
			}
		}
		os.Exit(code)
	case "help", "--help", "-h":
		printHelp()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		printHelp()
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Print(`gh-checkproxy — GitHub Checks API proxy for fine-grained tokens

SERVER COMMANDS (run on trusted host):
  gh-checkproxy config [flags]     Configure the proxy (interactive)
    --org <org>                      Restrict to this organization (optional)
    --port <port>                    HTTP listen port (default: 8080)
    --cache-ttl <duration>           Validation cache TTL (default: 5m)
  Token: $GH_CHECKPROXY_CLASSIC_TOKEN, reuse $GH_TOKEN (when classic), or enter interactively (masked)
  gh-checkproxy serve              Start the proxy server
  gh-checkproxy status             Show current configuration

CLIENT COMMANDS (run on agent machine):
  gh-checkproxy pr checks [<number>|<url>|<branch>] [flags]
    --repo <owner/repo>              Repository (auto-detected from git remote)
    --proxy-url <url>                Proxy URL (or $GH_CHECKPROXY_URL)
    --token <token>                  Fine-grained token (or $GH_TOKEN / $GITHUB_TOKEN)
    --watch                          Watch until checks complete
    --fail-fast                      Exit on first failure (requires --watch)
    --interval <duration>            Refresh interval in watch mode (default: 10s)
    --required                       Only show required checks

  Exit codes:
    0   All checks passed
    1   Some checks failed
    8   Checks still pending
`)
}
