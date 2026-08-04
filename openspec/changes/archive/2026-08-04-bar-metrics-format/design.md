## Context

The `outfit remote metrics` command currently outputs a verbose key-value table by default. When using `--watch` to monitor over time, each refresh appends below the previous output with a `---` separator, creating a growing scrollback. The network round-trip to the stats Lambda also causes a visible delay before each refresh appears.

## Goals / Non-Goals

**Goals:**
- Provide a compact, scannable default output with coloured progress bars for resource metrics
- Eliminate scrollback accumulation in watch mode by clearing and redrawing in place
- Remove the blank-frame flicker in watch mode by pre-rendering output before clearing

**Non-Goals:**
- Adding new metrics or changing what data is fetched from the Lambda
- Terminal width detection or responsive bar sizing
- Historical data or trend lines
- Interactive controls within watch mode

## Decisions

**Default format is `bar`.** The table format is verbose for repeated viewing. Bar format gives an at-a-glance view of resource usage, which is the common case when monitoring a remote instance. Table remains available as `--format=table`.

**ANSI escape codes for screen clearing.** `\033[2J\033[H` (clear screen + cursor home) is the standard approach. No external dependency needed — works in any modern terminal. We don't detect terminal capability because outfit is a dev tool where coloured output is expected.

**ANSI colours for bar fill.** Green (`\033[92m`) for healthy (≤80%), yellow (`\033[33m`) for warning (80–90%), red (`\033[31m`) for critical (>90%). The unfilled portion uses a lighter character (`░`) without colour so it's visually distinct but unobtrusive.

**Bar width of 25 columns.** Wide enough to be meaningful, narrow enough to fit comfortably alongside the label and percentage. Fixed width keeps the output stable across refreshes.

**Pre-render into `strings.Builder` before clearing.** The network fetch (HTTP to Lambda) is the slow operation. By rendering to a buffer first, the clear-and-paint is instantaneous — no blank screen between refreshes.

**All format functions accept `io.Writer`.** Refactoring from hardcoded `os.Stdout` to a writer parameter allows both normal output and buffered output (for watch mode) without duplicating format logic.

## Risks / Trade-offs

- [Terminals that don't support ANSI escapes will show raw escape sequences] → Mitigation: these are rare in modern dev environments; the existing codebase already uses ANSI colours elsewhere without capability detection
- [Watch mode clears the entire terminal, losing scrollback history] → Mitigation: intentional — the user can disable watch mode or use `--format=table` to preserve history if needed
