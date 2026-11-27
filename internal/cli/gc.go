package cli

import (
	"fmt"
	"io"

	"github.com/Taf0711/splice/internal/tempgc"
)

const gcHelpText = `Usage:
  splice gc [flags]

Removes stale temporary files that Splice left behind.
Removes Go build caches that were not used for 7 days.
Keeps the Go build cache total under the cap (10 GB by default).
Set SPLICE_GOCACHE_MAX_GB to change the cap.
Removes stale run directories of killed processes.

Flags:
      --dry-run  Show what would be removed. Delete nothing.
  -h, --help     Show this help
`

func runGC(args []string, stdout io.Writer, stderr io.Writer) int {
	dryRun := false
	for _, arg := range args {
		switch arg {
		case "--dry-run":
			dryRun = true
		case "-h", "--help":
			if _, err := fmt.Fprint(stdout, gcHelpText); err != nil {
				return exitCrash
			}
			return exitSuccess
		default:
			return writeExecUsageError(stderr, fmt.Sprintf("unknown gc argument %q", arg))
		}
	}

	report, err := tempgc.Collect(tempgc.Options{DryRun: dryRun})
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	if _, err := fmt.Fprintln(stdout, formatGCSummary(report, dryRun)); err != nil {
		return exitCrash
	}
	for _, line := range report.Errors {
		if _, err := fmt.Fprintln(stderr, line); err != nil {
			return exitCrash
		}
	}
	return exitSuccess
}

func formatGCSummary(report tempgc.Report, dryRun bool) string {
	verb := "Removed"
	if dryRun {
		verb = "Would remove"
	}
	line := fmt.Sprintf("%s %s (%s) and %s (%s). Kept %s (%s).",
		verb,
		counted(report.CachesRemoved, "Go build cache", "Go build caches"), humanBytes(report.CacheBytesFreed),
		counted(report.StaleDirsRemoved, "stale run directory", "stale run directories"), humanBytes(report.StaleBytesFreed),
		counted(report.CachesKept, "Go build cache", "Go build caches"), humanBytes(report.CacheBytesKept),
	)
	if dryRun {
		line += " Nothing was deleted."
	}
	return line
}

func counted(n int, singular string, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}

// humanBytes formats a byte count with binary units, as the rest of the CLI does.
func humanBytes(n int64) string {
	if n < 1<<10 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	value := float64(n)
	unit := -1
	for value >= 1<<10 && unit < len(units)-1 {
		value /= 1 << 10
		unit++
	}
	return fmt.Sprintf("%.1f %s", value, units[unit])
}
