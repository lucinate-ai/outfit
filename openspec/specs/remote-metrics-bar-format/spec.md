# Bar Metrics Format Specification

## Purpose

Define the bar graph output format for `spinloop remote metrics` with colour-coded resource utilization indicators.

## Requirements

### Requirement: Bar format output

The system SHALL support a `--format=bar` option that renders each resource series as a sparkline drawn from the history the on-instance daemon retains: a left-aligned label, one glyph per sample, and the latest value as a right-aligned percentage. The glyphs SHALL be Unicode block elements of one grade per utilisation level, so a series reads as a line of bars across the window. The series drawn SHALL be the same set the gauge format draws: CPU, RAM, each GPU's utilisation, and each GPU's memory where that GPU's current reading or retained history reports a memory total — a GPU that never reports one draws no memory series, with the same per-GPU labelling throughout.

#### Scenario: Bar format displays CPU utilization

- **WHEN** the user runs `spinloop remote metrics --format=bar` with a running instance that has CPU data and a retained history
- **THEN** the output includes a row labelled "CPU" whose glyphs are the sampled CPU utilisation across the window and whose trailing figure is the latest sample's percentage

#### Scenario: Bar format displays RAM utilization

- **WHEN** the user runs `spinloop remote metrics --format=bar` with a running instance that has memory data
- **THEN** the output includes a row labelled "RAM" whose glyphs are the sampled used/total memory ratio across the window and whose trailing figure is the latest ratio

#### Scenario: Bar format displays GPU utilization

- **WHEN** the user runs `spinloop remote metrics --format=bar` with a running instance that has GPU data whose readings carry a memory total
- **THEN** the output includes rows labelled "GPU util" and "GPU mem" (or "GPU N util"/"GPU N mem" for multiple GPUs), each drawn from the retained history

#### Scenario: Bar format omits a GPU memory series with no total

- **WHEN** the user runs `spinloop remote metrics --format=bar` with GPU data whose current reading and retained history report no memory total
- **THEN** the output includes "GPU util" and no "GPU mem" row

#### Scenario: Bar format header line

- **WHEN** the user runs `spinloop remote metrics --format=bar` with a running instance
- **THEN** the first line shows the environment, state, instance type, and model ID separated by double spaces

### Requirement: Colour thresholds

The sparkline's latest point SHALL be colour-coded based on utilization: green for values at or below 80%, yellow for values from 80% to 90%, and red for values above 90%. Every earlier point SHALL appear in the terminal's default colour, so the coloured point is the one to read.

#### Scenario: Green bar for low utilization

- **WHEN** the latest sample of a series is 70%
- **THEN** the sparkline's final glyph appears in green and the earlier glyphs appear in the terminal's default colour

#### Scenario: Yellow bar for high utilization

- **WHEN** the latest sample of a series is 85%
- **THEN** the sparkline's final glyph appears in yellow

#### Scenario: Red bar for critical utilization

- **WHEN** the latest sample of a series is 95%
- **THEN** the sparkline's final glyph appears in red

### Requirement: Gauge format is default

The system SHALL use gauge format as the default output when no `--format` flag is specified.

#### Scenario: Default format is gauge

- **WHEN** the user runs `spinloop remote metrics` without `--format`
- **THEN** the output is in gauge format

### Requirement: Bar format with stopped instance

When the instance is not running, bar format SHALL show the header line with environment, state, instance type, and model, and — where the daemon's retained history survives the stop — the series drawn from it, ending at the stop. The retained history answers "what was this engine doing until it stopped", the same question the last-active figure answers, and the header already carries the state. When no history is available, the format SHALL fall back to the gauge drawing of the current reading per the no-history rule — which for a stopped engine, whose current reading carries no resource figures, means no resource series at all. When a last-active time is known it SHALL still be shown, in the same place it occupies for a running instance.

#### Scenario: Stopped instance shows header only

- **WHEN** the user runs `spinloop remote metrics --format=bar` and the instance is stopped with no retained history and no recorded activity
- **THEN** the output shows the header with state "stopped" and no resource series

#### Scenario: Stopped instance still reports its last activity

- **WHEN** the user runs `spinloop remote metrics --format=bar`, the instance is stopped, and a last-active time is known
- **THEN** the output shows the header, the last-active line, and the series drawn from the retained history where one exists

#### Scenario: Stopped instance shows its history

- **WHEN** the user runs `spinloop remote metrics --format=bar` and the instance's engine has been stopped after running, with a retained history
- **THEN** the output shows the series as sparklines drawn from the readings taken before the stop, ending at the stop

### Requirement: Last-active line in bar format

Bar format SHALL show the last-active figure on its own line, immediately
below the header line and above the resource bars, so it reads as a fact about
the endpoint rather than as another utilisation reading. It SHALL NOT be drawn
as a bar: it is an elapsed time with no ceiling to fill against, and a bar
would imply one.

The line SHALL be omitted entirely when no last-active time is known, rather
than shown empty or zeroed.

#### Scenario: The figure sits under the header

- **WHEN** the user runs `spinloop remote metrics --format=bar` against a
  running endpoint whose engine has served work
- **THEN** the line after the header shows how long ago that was, and the
  resource bars follow it

#### Scenario: No activity, no line

- **WHEN** bar format renders an endpoint with no known last-active time
- **THEN** the output goes straight from the header to the resource bars

### Requirement: Gauge format

The system SHALL support a `--format=gauge` option that renders each resource series as a horizontal progress gauge: a left-aligned label, a filled portion using block characters, an unfilled portion using light shade characters, and a right-aligned percentage value. The gauge draws the current reading only — it carries no history. The series drawn SHALL be CPU, RAM, each GPU's utilisation, and each GPU's memory where the current reading reports a memory total — a GPU that reports none draws no memory gauge — with the same labels the bar format uses. The gauge fill SHALL be colour-coded on the bar format's thresholds: green for values at or below 80%, yellow for values from 80% to 90%, and red for values above 90%, with the colour reset after the filled portion so the unfilled characters and percentage appear in the terminal's default colour.

#### Scenario: Gauge format displays CPU utilization

- **WHEN** the user runs `spinloop remote metrics --format=gauge` with a running instance that has CPU data
- **THEN** the output includes a gauge labelled "CPU" with filled and unfilled segments proportional to the current utilization

#### Scenario: Gauge format displays GPU utilisation

- **WHEN** the user runs `spinloop remote metrics --format=gauge` with a running instance that has GPU data whose readings carry a memory total
- **THEN** the output includes gauges labelled "GPU util" and "GPU mem" (or "GPU N util"/"GPU N mem" for multiple GPUs)

#### Scenario: Gauge omits a GPU memory gauge with no total

- **WHEN** the user runs `spinloop remote metrics --format=gauge` with a GPU whose current reading reports no memory total
- **THEN** the output includes "GPU util" and no "GPU mem" gauge

#### Scenario: Gauge colours the fill

- **WHEN** a gauge's current value is 95%
- **THEN** its filled segment appears in red, and its unfilled segment and percentage appear in the terminal's default colour

### Requirement: Bar draws the retained history

Bar format SHALL draw each series from the history the on-instance daemon retains: readings taken at the sampler's cadence while the engine ran, covering at most the last 10 minutes. The sparkline SHALL show every sample the window holds, downsampled to the draw width where the window holds more samples than the width allows; downsampled points SHALL preserve the window's extremes rather than averaging them away.

The format SHALL be usable in one-shot mode: the history comes from the daemon, not from the command's own polling, so `--format=bar` without `--watch` draws the same window `--watch` would.

Where the daemon reports no history — a daemon that predates the feature, or an engine with no reading yet — bar format SHALL draw each series from the current reading alone, in the gauge's filled style, so a bar format that is asked for still shows the current level and a pre-history daemon renders what it can.

#### Scenario: One-shot bar shows the daemon's window

- **WHEN** the user runs `spinloop remote metrics --format=bar` without `--watch` against an engine that has been running
- **THEN** the output shows each series as a sparkline covering up to the last 10 minutes of the daemon's retained samples

#### Scenario: More samples than width are downsampled

- **WHEN** the retained window holds more samples than the draw width
- **THEN** the sparkline shows one glyph per column of the width, and a spike inside a downsampled range is still visible rather than smoothed away

#### Scenario: No history falls back to the gauge drawing

- **WHEN** the user runs `spinloop remote metrics --format=bar` against a daemon that reports no history
- **THEN** each series is drawn from the current reading in the gauge's filled style

### Requirement: Sparkline never draws a full block

The sparkline SHALL map a series' 0–100% values to Unicode block elements of one
grade per utilisation level, capped at the seven-eighths block (`▇`): the highest
value in a series renders as `▇`, and no value renders as the full block (`█`).
Capping the tallest glyph one grade short of full height leaves the top eighth of
each cell unfilled, so two adjacent series at their maximum read as separate bars
with a visible gap between the bottom of one row and the top of the next, instead
of merging into a single solid block. The exact value is carried by the trailing
percentage, so a glyph may sit one grade below full height without losing
information.

#### Scenario: A value at the top of the range caps at seven-eighths

- **WHEN** a sample in a series reaches 100%
- **THEN** its glyph is the seven-eighths block (`▇`), not the full block (`█`)

#### Scenario: No sparkline glyph is a full block

- **WHEN** the sparkline draws any series from its retained samples
- **THEN** none of the glyphs it draws is the full block (`█`)

#### Scenario: Adjacent maxed rows keep a visible gap

- **WHEN** two adjacent series are both at their maximum across the window
- **THEN** each row's tallest glyph leaves the top eighth of its cell unfilled, so a blank strip separates the rows rather than the rows reading as one solid block
