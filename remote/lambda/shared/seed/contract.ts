/**
 * The contract between the control plane and the seeder that runs on the
 * instance. Both sides import this file — the Lambda renders a SeedJob into
 * user-data, the seeder parses one back — so the wire shape cannot drift
 * between the thing that writes it and the thing that reads it.
 *
 * It also fixes the CloudWatch vocabulary: the namespace, the metric names and
 * the phase values are constants here rather than string literals scattered
 * across the seeder that emits them and the status reader that interprets them.
 */

import type { Runner } from '../deploy-config';

/**
 * Which of a model repository's files an engine needs. Declared per runner
 * (see RunnerSpec.seedSelection) rather than expressed as a boot-script
 * fragment, so the seeder stays runner-agnostic and adding a runner never
 * means writing shell.
 *
 * Patterns are glob-style over the repository-relative path, matched by
 * matchesSelection below — not regular expressions, because they are written
 * by hand in runner specs where `*` is the expected spelling.
 */
export interface SeedSelection {
  /** Paths to take. A file must match at least one to be transferred. */
  include: string[];
  /** Paths to drop even when included (e.g. an unnamed mmproj projector). */
  exclude?: string[];
  /**
   * Set when the engine serves exactly one file: the selection must resolve to
   * a single file, which is stored under this name. More than one match fails
   * the seed rather than picking one — the old boot script warned and took the
   * first, which silently shipped half of a split quant.
   */
  expectSingle?: string;
  /**
   * Extra files the deployment named, taken by *exact* repository filename and
   * stored under a fixed name — companion weights, such as a speculative
   * drafter or a projector.
   *
   * They cannot ride on `include`, for two reasons. The quant glob does not
   * reach them (a `kquant-dynamic` quant whose drafter is
   * `dflash-kquant.gguf`), and `expectSingle` must still see exactly one
   * candidate for the main weights, so a companion has to be selected apart
   * from it rather than competing with it.
   *
   * A named companion the repository does not have fails the seed. That is
   * deliberate: the alternative is an instance starting minutes later with a
   * flag pointing at a file that was never fetched.
   */
  companions?: { storeAs: string; file: string }[];
}

/**
 * One seed's complete instructions. Rendered into user-data as JSON, so it
 * must stay small (the 16 KB user-data limit) and must contain no secret —
 * the Hugging Face token is named by ARN and read on the instance.
 */
export interface SeedJob {
  /** The seed's identity. Also its log stream prefix and its instance tag. */
  seedId: string;
  /** Which engine these weights are for. A metric dimension, so low-cardinality. */
  runner: Runner;
  /** Hugging Face repo id. */
  modelId: string;
  /** GGUF quant tag, or empty. Part of the identity, not of the fetch itself. */
  quant: string;
  /**
   * The revision to fetch. Empty means "whatever the repo's default branch
   * resolves to", and the resolved commit is recorded in the manifest either
   * way, so a seed is always identifiable after the fact.
   */
  revision: string;
  bucket: string;
  /** Key prefix, always ending in `/`. Derived by weightsPrefixFor. */
  prefix: string;
  selection: SeedSelection;
  /** Empty when no token is configured (public repos only). */
  hfSecretArn: string;
  region: string;
  /** Where the seeder appends its EMF records for the CloudWatch agent to ship. */
  recordPath: string;
  /** Transfer tuning, resolved by the Lambda so the instance holds no policy. */
  partSizeBytes: number;
  partConcurrency: number;
  /** Attempts per part before a file falls back to disk staging. */
  partAttempts: number;
}

/** The object whose presence means "these weights are complete". */
export const MANIFEST_NAME = '_seed.json';

/** The manifest key for a weights prefix. */
export function manifestKey(prefix: string): string {
  return `${prefix}${MANIFEST_NAME}`;
}

export interface ManifestFile {
  /** Path under the weights prefix, i.e. the S3 key with the prefix removed. */
  path: string;
  size: number;
  /** The sha256 the source published, verified against what was transferred. */
  sha256: string;
}

/**
 * What a completed seed leaves behind. Written last, once every file has
 * transferred and verified, which is what makes its presence a sound
 * completeness check — unlike a weights file, whose write order is a property
 * of the transfer rather than a guarantee.
 */
export interface SeedManifest {
  modelId: string;
  /** The resolved commit sha, never a branch name. */
  revision: string;
  runner: Runner;
  quant: string;
  seededAt: string;
  seedId: string;
  /** Version of the seeder that produced this, and the runtime it ran on. */
  seederVersion: string;
  seederNodeVersion: string;
  files: ManifestFile[];
  totalBytes: number;
}

/** CloudWatch namespace for the extracted metrics. */
export const SEED_NAMESPACE = 'cloud-vm-llm/seed';

/** Log group the seeder's records are shipped to. */
export const SEED_LOG_GROUP = '/cloud-vm-llm/seed';

/**
 * Where a seed's records live. One stream per attempt, so a re-seed's records
 * are never interleaved with the attempt before it: the status read takes the
 * most recently written stream under the seed's prefix.
 */
export function seedLogStream(seedId: string, instanceId: string): string {
  return `${seedId}/${instanceId}`;
}

/**
 * The phases a seed moves through. Carried as a record property, deliberately
 * NOT as a metric: an enum encoded as a number is unreadable on a graph and
 * meaningless when averaged.
 */
export const SEED_PHASES = [
  'starting',
  'resolving',
  'transferring',
  'finalising',
  'succeeded',
  'failed',
  'stopped',
] as const;
export type SeedPhase = (typeof SEED_PHASES)[number];

/** Phases from which a seed will make no further progress. */
export const TERMINAL_PHASES = ['succeeded', 'failed', 'stopped'] as const;
export type TerminalPhase = (typeof TERMINAL_PHASES)[number];

export function isTerminalPhase(phase: string): phase is TerminalPhase {
  return (TERMINAL_PHASES as readonly string[]).includes(phase);
}

export function isSeedPhase(value: unknown): value is SeedPhase {
  return typeof value === 'string' && (SEED_PHASES as readonly string[]).includes(value);
}

/**
 * Metric names the records declare. Terminal outcomes are counts so an alarm
 * can be raised on them without reading a log line.
 */
export const SEED_METRICS = {
  bytesTransferred: 'BytesTransferred',
  filesCompleted: 'FilesCompleted',
  progressPercent: 'ProgressPercent',
  started: 'Started',
  succeeded: 'Succeeded',
  failed: 'Failed',
  stopped: 'Stopped',
  durationSeconds: 'DurationSeconds',
} as const;

/**
 * One record the seeder appends. The `_aws` block is what makes CloudWatch
 * extract metrics from it (Embedded Metric Format); everything alongside it is
 * an ordinary property — queryable, and free of the per-metric cost that a
 * dimension would carry.
 *
 * `SeedId` is deliberately a property and not a dimension: dimensioning on it
 * would mint a permanently billed custom metric for every model ever seeded.
 */
export interface SeedRecord {
  _aws?: {
    Timestamp: number;
    CloudWatchMetrics: {
      Namespace: string;
      Dimensions: string[][];
      Metrics: { Name: string; Unit?: string }[];
    }[];
  };
  SeedId: string;
  /** The one metric dimension. Bounded by RUNNERS, so metric count is bounded. */
  Runner: Runner;
  Phase: SeedPhase;
  ModelId: string;
  Revision?: string;
  InstanceId?: string;
  Message?: string;
  Error?: string;
  BytesTotal?: number;
  BytesDone?: number;
  FilesTotal?: number;
  FilesDone?: number;
  CurrentFile?: string;
  ProgressPercent?: number;
  DurationSeconds?: number;
  /** Set when a file had to fall back to disk staging, for later diagnosis. */
  StagedFiles?: number;
  [key: string]: unknown;
}

/**
 * Parse a record the seeder wrote. Returns null for anything unparseable, so a
 * truncated final line (the agent can ship a partial write) degrades the status
 * read to the previous record instead of failing it.
 */
export function parseSeedRecord(line: string): SeedRecord | null {
  try {
    const parsed = JSON.parse(line) as Record<string, unknown>;
    if (typeof parsed.SeedId !== 'string' || !isSeedPhase(parsed.Phase)) {
      return null;
    }
    return parsed as unknown as SeedRecord;
  } catch {
    return null;
  }
}

/**
 * Whether a repository path is selected. Globs support `*` (any run of
 * characters, path separators included) and `?`; everything else is literal.
 * Exclusions win over inclusions.
 */
export function matchesSelection(path: string, selection: SeedSelection): boolean {
  const excluded = (selection.exclude ?? []).some((pattern) => globMatches(path, pattern));
  if (excluded) {
    return false;
  }
  return selection.include.some((pattern) => globMatches(path, pattern));
}

/** Case-insensitive glob match — Hugging Face paths mix cases inconsistently. */
export function globMatches(path: string, pattern: string): boolean {
  const escaped = pattern.replace(/[.+^${}()|[\]\\]/g, '\\$&');
  const expanded = escaped.replace(/\*/g, '.*').replace(/\?/g, '.');
  return new RegExp(`^${expanded}$`, 'i').test(path);
}

/**
 * Apply a selection to a repository listing, enforcing `expectSingle`.
 * Throws with the candidates named when a single-file engine's selection is
 * ambiguous — the caller surfaces that as the seed's failure message.
 */
export function applySelection(
  paths: string[],
  selection: SeedSelection,
): { path: string; storeAs: string }[] {
  // Companions are resolved first and removed from the pool, so a named
  // companion can never also be a candidate for the main weights — which is
  // what lets expectSingle stay an exact "one file" check.
  const companions = (selection.companions ?? []).map(({ storeAs, file }) => {
    const path = paths.find((p) => p === file || p.endsWith(`/${file}`));
    if (!path) {
      throw new Error(
        `companion ${JSON.stringify(file)} (stored as ${storeAs}) is not in the repository — ` +
          'check the filename, or remove it from the deployment',
      );
    }
    return { path, storeAs };
  });
  const claimed = new Set(companions.map((c) => c.path));

  const matched = paths
    .filter((path) => !claimed.has(path) && matchesSelection(path, selection))
    .sort();
  if (matched.length === 0) {
    throw new Error(
      `no files in the repository match ${JSON.stringify(selection.include)}` +
        (selection.exclude?.length ? ` after excluding ${JSON.stringify(selection.exclude)}` : ''),
    );
  }
  if (selection.expectSingle) {
    if (matched.length > 1) {
      throw new Error(
        `expected exactly one file for this runner but ${matched.length} match: ` +
          `${matched.join(', ')} — narrow the quant, or the model needs support for split files`,
      );
    }
    return [{ path: matched[0], storeAs: selection.expectSingle }, ...companions];
  }
  return [...matched.map((path) => ({ path, storeAs: path })), ...companions];
}
