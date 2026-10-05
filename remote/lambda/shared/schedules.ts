/**
 * Schedules: when an environment is started and stopped without anyone asking.
 *
 * An environment holds a list of schedules, each a five-field cron expression,
 * an IANA time zone and an action. The list is stored in an SSM parameter and
 * mirrored into EventBridge Scheduler schedules, which fire the start and stop
 * Lambdas with a `ScheduledRunEvent`. Everything here is pure (no AWS calls),
 * so the Lambda that owns the schedules and the two that run them share it.
 */

import { createHash } from 'node:crypto';

export const SCHEDULE_ACTIONS = ['start', 'stop'] as const;
export type ScheduleAction = (typeof SCHEDULE_ACTIONS)[number];

export interface Schedule {
  action: ScheduleAction;
  /** Five fields: minute, hour, day of month, month, day of week. */
  cron: string;
  /** IANA zone name, e.g. `Europe/London`. */
  timezone: string;
}

/** The `source` of the event EventBridge Scheduler sends to the start and stop Lambdas. */
export const SCHEDULE_EVENT_SOURCE = 'spinloop.schedule';

export interface ScheduledRunEvent {
  source: typeof SCHEDULE_EVENT_SOURCE;
  action: ScheduleAction;
  environment: string;
}

export function isScheduledRunEvent(event: unknown): event is ScheduledRunEvent {
  return (
    typeof event === 'object' &&
    event !== null &&
    (event as { source?: unknown }).source === SCHEDULE_EVENT_SOURCE
  );
}

/** More than this on one environment is almost certainly a mistake, and bounds the Scheduler calls per request. */
export const MAX_SCHEDULES = 20;

export const DEFAULT_TIMEZONE = 'UTC';

/** Raised for input the caller can fix; the Lambda maps it to a 400. */
export class ScheduleError extends Error {}

/** SSM parameter holding an environment's schedule list. */
export function schedulesParam(env: string): string {
  return `/cloud-vm-llm/${env}/schedules`;
}

/**
 * The prefix of the Scheduler schedule names belonging to one environment.
 * Schedule names are limited to 64 characters and an environment name can be
 * that long on its own, so the prefix is a hash of the name; the environment
 * itself travels in each schedule's input.
 */
export function schedulerNamePrefix(env: string): string {
  return `e-${createHash('sha256').update(env).digest('hex').slice(0, 16)}-`;
}

const FIELD_RANGES: ReadonlyArray<{ name: string; min: number; max: number }> = [
  { name: 'minute', min: 0, max: 59 },
  { name: 'hour', min: 0, max: 23 },
  { name: 'day of month', min: 1, max: 31 },
  { name: 'month', min: 1, max: 12 },
  { name: 'day of week', min: 0, max: 7 },
];

const WEEKDAY_NAMES = ['SUN', 'MON', 'TUE', 'WED', 'THU', 'FRI', 'SAT'];

export interface ParsedCron {
  minutes: number[];
  hours: number[];
  daysOfMonth: number[];
  months: number[];
  /** 0 (Sunday) to 6 (Saturday). */
  daysOfWeek: number[];
  /** True when the field was `*` (or an equivalent full range), so it does not restrict. */
  anyDayOfMonth: boolean;
  anyDayOfWeek: boolean;
  /** Source text of the day-of-week field, for rendering. */
  dayOfWeekField: string;
}

function invalid(expr: string, why: string): ScheduleError {
  return new ScheduleError(`invalid cron expression ${JSON.stringify(expr)}: ${why}`);
}

function parseField(expr: string, field: string, index: number): number[] {
  const { name, min, max } = FIELD_RANGES[index];
  const values = new Set<number>();
  for (const part of field.split(',')) {
    const [rangePart, stepPart, extra] = part.split('/');
    if (extra !== undefined || rangePart === '') {
      throw invalid(expr, `bad ${name} field ${JSON.stringify(field)}`);
    }
    let step = 1;
    if (stepPart !== undefined) {
      if (!/^\d+$/.test(stepPart) || Number(stepPart) < 1) {
        throw invalid(expr, `bad step in ${name} field ${JSON.stringify(field)}`);
      }
      step = Number(stepPart);
    }
    let lo: number;
    let hi: number;
    if (rangePart === '*') {
      lo = min;
      hi = max;
    } else {
      const m = /^(\d+)(?:-(\d+))?$/.exec(rangePart);
      if (!m) {
        throw invalid(expr, `bad ${name} field ${JSON.stringify(field)}`);
      }
      lo = Number(m[1]);
      hi = m[2] === undefined ? (stepPart === undefined ? lo : max) : Number(m[2]);
    }
    if (lo < min || hi > max || lo > hi) {
      throw invalid(expr, `${name} must be within ${min}-${max}`);
    }
    for (let v = lo; v <= hi; v += step) {
      values.add(v);
    }
  }
  return [...values].sort((a, b) => a - b);
}

/** Parse a five-field cron expression. Names (`MON`, `JAN`) are not accepted. */
export function parseCron(expr: string): ParsedCron {
  const fields = expr.trim().split(/\s+/);
  if (fields.length !== 5) {
    throw invalid(expr, 'expected five fields (minute hour day-of-month month day-of-week)');
  }
  const [minutes, hours, daysOfMonth, months, dow] = fields.map((f, i) => parseField(expr, f, i));
  const daysOfWeek = [...new Set(dow.map((d) => d % 7))].sort((a, b) => a - b);
  const anyDayOfMonth = daysOfMonth.length === 31;
  const anyDayOfWeek = daysOfWeek.length === 7;
  if (!anyDayOfMonth && !anyDayOfWeek) {
    throw invalid(expr, 'set either the day of month or the day of week, not both');
  }
  return {
    minutes,
    hours,
    daysOfMonth,
    months,
    daysOfWeek,
    anyDayOfMonth,
    anyDayOfWeek,
    dayOfWeekField: fields[4],
  };
}

/**
 * The EventBridge Scheduler form of a five-field expression: six fields (year
 * added), `?` in whichever day field is not used, and weekday names, since the
 * numbering of weekdays differs from classic cron (Scheduler counts Sunday as 1).
 */
export function toSchedulerCron(expr: string): string {
  const parsed = parseCron(expr);
  const [minute, hour, dom, month] = expr.trim().split(/\s+/);
  const dayOfMonth = parsed.anyDayOfMonth ? (parsed.anyDayOfWeek ? '*' : '?') : dom;
  const dayOfWeek = parsed.anyDayOfWeek
    ? '?'
    : parsed.daysOfWeek.map((d) => WEEKDAY_NAMES[d]).join(',');
  return `cron(${minute} ${hour} ${dayOfMonth} ${month} ${dayOfWeek} *)`;
}

export function isValidTimezone(zone: string): boolean {
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: zone });
    return true;
  } catch {
    return false;
  }
}

/**
 * Validate an untrusted schedule list, filling the default time zone. Throws a
 * `ScheduleError` naming the first problem.
 */
export function validateSchedules(input: unknown): Schedule[] {
  if (!Array.isArray(input)) {
    throw new ScheduleError('schedules must be a list');
  }
  if (input.length > MAX_SCHEDULES) {
    throw new ScheduleError(`at most ${MAX_SCHEDULES} schedules per environment`);
  }
  return input.map((raw, i) => {
    if (typeof raw !== 'object' || raw === null) {
      throw new ScheduleError(`schedule ${i + 1} must be an object`);
    }
    const { action, cron, timezone } = raw as Record<string, unknown>;
    if (!(SCHEDULE_ACTIONS as readonly unknown[]).includes(action)) {
      throw new ScheduleError(
        `schedule ${i + 1}: unknown action ${JSON.stringify(action)}; accepted: ${SCHEDULE_ACTIONS.join(', ')}`,
      );
    }
    if (typeof cron !== 'string') {
      throw new ScheduleError(`schedule ${i + 1}: cron must be a string`);
    }
    parseCron(cron);
    const zone = timezone === undefined || timezone === '' ? DEFAULT_TIMEZONE : timezone;
    if (typeof zone !== 'string' || !isValidTimezone(zone)) {
      throw new ScheduleError(`schedule ${i + 1}: unknown time zone ${JSON.stringify(zone)}`);
    }
    return { action: action as ScheduleAction, cron: cron.trim().split(/\s+/).join(' '), timezone: zone };
  });
}

interface WallTime {
  year: number;
  month: number;
  day: number;
  hour: number;
  minute: number;
}

function wallTimeIn(utcMs: number, zone: string): WallTime {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone: zone,
    hourCycle: 'h23',
    year: 'numeric',
    month: 'numeric',
    day: 'numeric',
    hour: 'numeric',
    minute: 'numeric',
  }).formatToParts(new Date(utcMs));
  const get = (type: string) => Number(parts.find((p) => p.type === type)?.value);
  return {
    year: get('year'),
    month: get('month'),
    day: get('day'),
    hour: get('hour'),
    minute: get('minute'),
  };
}

function wallAsUtc(w: WallTime): number {
  return Date.UTC(w.year, w.month - 1, w.day, w.hour, w.minute);
}

/**
 * The instant a wall-clock time falls at in a zone, or null when the zone's
 * clocks skip that time (the hour lost when they go forward). When the clocks
 * repeat an hour, the first occurrence is returned.
 */
function instantOf(w: WallTime, zone: string): number | null {
  const asUtc = wallAsUtc(w);
  // The offset around the wanted time, probed a day either side so a change at
  // the wanted time itself cannot hide it.
  const candidates = new Set<number>();
  for (const probe of [asUtc - 86_400_000, asUtc, asUtc + 86_400_000]) {
    candidates.add(wallAsUtc(wallTimeIn(probe, zone)) - probe);
  }
  const matches = [...candidates]
    .map((offset) => asUtc - offset)
    .filter((utc) => wallAsUtc(wallTimeIn(utc, zone)) === asUtc)
    .sort((a, b) => a - b);
  return matches.length > 0 ? matches[0] : null;
}

/**
 * The next instant after `after` that the expression matches in the zone, or
 * null when there is none within the next two years.
 */
export function nextRun(expr: string, zone: string, after: Date): Date | null {
  const cron = parseCron(expr);
  const start = wallTimeIn(after.getTime(), zone);
  const dayMs = 86_400_000;
  const firstDay = Date.UTC(start.year, start.month - 1, start.day);
  for (let n = 0; n < 732; n++) {
    const day = new Date(firstDay + n * dayMs);
    const month = day.getUTCMonth() + 1;
    if (!cron.months.includes(month)) {
      continue;
    }
    const dayOk = cron.anyDayOfMonth
      ? cron.daysOfWeek.includes(day.getUTCDay())
      : cron.daysOfMonth.includes(day.getUTCDate());
    if (!dayOk) {
      continue;
    }
    for (const hour of cron.hours) {
      for (const minute of cron.minutes) {
        const instant = instantOf(
          {
            year: day.getUTCFullYear(),
            month,
            day: day.getUTCDate(),
            hour,
            minute,
          },
          zone,
        );
        if (instant !== null && instant > after.getTime()) {
          return new Date(instant);
        }
      }
    }
  }
  return null;
}

/** The next time each action fires across a list of schedules. */
export function nextRuns(
  schedules: Schedule[],
  after: Date,
): { start: string | null; stop: string | null } {
  const earliest = (action: ScheduleAction): string | null => {
    let best: Date | null = null;
    for (const s of schedules.filter((x) => x.action === action)) {
      const next = nextRun(s.cron, s.timezone, after);
      if (next && (!best || next < best)) {
        best = next;
      }
    }
    return best ? best.toISOString() : null;
  };
  return { start: earliest('start'), stop: earliest('stop') };
}
