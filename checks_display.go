package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"text/tabwriter"
	"time"
)

// ANSI escape codes (skipped when output is not a TTY).
const (
	ansiReset  = "\033[0m"
	ansiGreen  = "\033[32m"
	ansiRed    = "\033[31m"
	ansiYellow = "\033[33m"
	ansiGray   = "\033[90m"
	ansiBold   = "\033[1m"
)

// isTTY reports whether stdout is a terminal.
func isTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// check mirrors the fields from the gh CLI aggregate.go check struct.
type check struct {
	Name        string
	State       string
	StartedAt   time.Time
	CompletedAt time.Time
	Link        string
	Bucket      string // "pass", "fail", "pending", "skipping", "cancel"
	Event       string
	Workflow    string
	Description string
}

// checkCounts tallies check states.
type checkCounts struct {
	Failed   int
	Passed   int
	Pending  int
	Skipping int
	Canceled int
}

// sortChecks sorts checks: fail first, then pending, then pass/skip/cancel, then by name.
func sortChecks(checks []check) {
	sort.Slice(checks, func(i, j int) bool {
		bi, bj := checks[i].Bucket, checks[j].Bucket
		ni, nj := checks[i].Name, checks[j].Name
		li, lj := checks[i].Link, checks[j].Link
		if bi == bj {
			if ni == nj {
				return li < lj
			}
			return ni < nj
		}
		return (bi == "fail") || (bi == "pending" && bj != "fail")
	})
}

// printSummary writes the summary line (only in TTY mode).
func printSummary(out io.Writer, counts checkCounts, tty bool) {
	if !tty {
		return
	}
	total := counts.Failed + counts.Passed + counts.Skipping + counts.Pending + counts.Canceled
	if total == 0 {
		return
	}

	var headline string
	switch {
	case counts.Failed > 0:
		headline = ansiRed + ansiBold + "Some checks were not successful" + ansiReset
	case counts.Pending > 0:
		headline = ansiYellow + ansiBold + "Some checks are still pending" + ansiReset
	case counts.Canceled > 0:
		headline = ansiGray + ansiBold + "Some checks were cancelled" + ansiReset
	default:
		headline = ansiGreen + ansiBold + "All checks were successful" + ansiReset
	}

	tallies := fmt.Sprintf("%d cancelled, %d failing, %d successful, %d skipped, and %d pending checks",
		counts.Canceled, counts.Failed, counts.Passed, counts.Skipping, counts.Pending)

	fmt.Fprintf(out, "%s\n%s\n\n", headline, tallies)
}

// printTable renders the checks as a table. TTY output uses colors and symbols;
// non-TTY output uses plain tab-separated columns suitable for scripting.
func printTable(out io.Writer, checks []check, tty bool) {
	sortChecks(checks)
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)

	if tty {
		fmt.Fprintf(tw, "\t%s\t%s\t%s\t%s\n", "NAME", "DESCRIPTION", "ELAPSED", "URL")
		for _, c := range checks {
			mark, color := markForBucket(c.Bucket, tty)
			elapsed := elapsedStr(c.StartedAt, c.CompletedAt)

			name := ""
			if c.Workflow != "" {
				name = c.Workflow + "/"
			}
			name += c.Name
			if c.Event != "" {
				name += " (" + c.Event + ")"
			}

			fmt.Fprintf(tw, "%s%s%s\t%s\t%s\t%s\t%s\n",
				color, mark, ansiReset,
				name, c.Description, elapsed, c.Link,
			)
		}
	} else {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", "NAME", "STATUS", "ELAPSED", "URL", "DESCRIPTION")
		for _, c := range checks {
			elapsed := elapsedStr(c.StartedAt, c.CompletedAt)
			status := c.Bucket
			if status == "cancel" {
				status = "fail"
			}
			if elapsed == "" {
				elapsed = "0"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
				c.Name, status, elapsed, c.Link, c.Description)
		}
	}
	_ = tw.Flush()
}

// markForBucket returns the status symbol and its ANSI color for a given bucket.
func markForBucket(bucket string, tty bool) (mark, color string) {
	if !tty {
		return bucket, ""
	}
	switch bucket {
	case "fail":
		return "X", ansiRed
	case "pending":
		return "*", ansiYellow
	case "skipping", "cancel":
		return "-", ansiGray
	default: // pass
		return "✓", ansiGreen
	}
}

// elapsedStr formats the elapsed time between start and completion.
func elapsedStr(started, completed time.Time) string {
	if started.IsZero() || completed.IsZero() {
		return ""
	}
	e := completed.Sub(started)
	if e <= 0 {
		return ""
	}
	return e.Round(time.Second).String()
}
