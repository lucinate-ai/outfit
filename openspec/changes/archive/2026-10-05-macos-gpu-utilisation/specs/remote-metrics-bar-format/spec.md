# Spec Delta

## MODIFIED Requirements

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
