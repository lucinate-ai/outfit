import { describe, expect, it } from 'vitest';
import {
  isScheduledRunEvent,
  nextRun,
  nextRuns,
  parseCron,
  ScheduleError,
  schedulerNamePrefix,
  toSchedulerCron,
  validateSchedules,
} from '../lambda/shared/schedules';

describe('toSchedulerCron', () => {
  it.each([
    ['0 8 * * 1-5', 'cron(0 8 ? * MON,TUE,WED,THU,FRI *)'],
    ['30 18 * * *', 'cron(30 18 * * ? *)'],
    ['0 9 1 * *', 'cron(0 9 1 * ? *)'],
    ['*/15 * * * *', 'cron(*/15 * * * ? *)'],
    ['0 0 * * 0', 'cron(0 0 ? * SUN *)'],
    ['0 0 * * 7', 'cron(0 0 ? * SUN *)'],
    ['0 8 * 1,6 6,0', 'cron(0 8 ? 1,6 SUN,SAT *)'],
  ])('converts %s', (input, want) => {
    expect(toSchedulerCron(input)).toBe(want);
  });

  it('rejects a day of month and a day of week together', () => {
    expect(() => toSchedulerCron('0 8 1 * 1')).toThrow(/not both/);
  });
});

describe('parseCron', () => {
  it.each([
    '',
    '* * * *',
    '* * * * * *',
    '60 * * * *',
    '* 24 * * *',
    '* * 0 * *',
    '* * * 13 *',
    '* * * * 8',
    'a * * * *',
    '*/0 * * * *',
    '5-1 * * * *',
    '* * * * MON',
  ])('rejects %j and names it', (expr) => {
    expect(() => parseCron(expr)).toThrow(ScheduleError);
    expect(() => parseCron(expr)).toThrow(JSON.stringify(expr));
  });

  it('expands lists, ranges and steps', () => {
    const c = parseCron('0,30 8-10 * 1-3 */2');
    expect(c.minutes).toEqual([0, 30]);
    expect(c.hours).toEqual([8, 9, 10]);
    expect(c.months).toEqual([1, 2, 3]);
    expect(c.daysOfWeek).toEqual([0, 2, 4, 6]);
  });
});

describe('validateSchedules', () => {
  it('fills the default time zone and normalises spacing', () => {
    expect(validateSchedules([{ action: 'start', cron: '0  8 * * 1-5' }])).toEqual([
      { action: 'start', cron: '0 8 * * 1-5', timezone: 'UTC' },
    ]);
  });

  it('accepts an empty list', () => {
    expect(validateSchedules([])).toEqual([]);
  });

  it('rejects a non-list, an unknown action, a bad zone and a bad cron', () => {
    expect(() => validateSchedules({})).toThrow(/must be a list/);
    expect(() => validateSchedules([{ action: 'pause', cron: '0 8 * * *' }])).toThrow(/unknown action/);
    expect(() =>
      validateSchedules([{ action: 'start', cron: '0 8 * * *', timezone: 'Mars/Olympus' }]),
    ).toThrow(/unknown time zone/);
    expect(() => validateSchedules([{ action: 'start', cron: 'nope' }])).toThrow(/"nope"/);
  });

  it('rejects too many schedules', () => {
    const many = Array.from({ length: 21 }, () => ({ action: 'start', cron: '0 8 * * *' }));
    expect(() => validateSchedules(many)).toThrow(/at most/);
  });
});

describe('nextRun', () => {
  it('follows local time across daylight saving', () => {
    const summer = nextRun('0 8 * * *', 'Europe/London', new Date('2026-07-01T12:00:00Z'));
    const winter = nextRun('0 8 * * *', 'Europe/London', new Date('2026-12-01T12:00:00Z'));
    expect(summer?.toISOString()).toBe('2026-07-02T07:00:00.000Z');
    expect(winter?.toISOString()).toBe('2026-12-02T08:00:00.000Z');
  });

  it('skips weekends for a weekday schedule', () => {
    // Friday 2026-10-09 evening; the next 08:00 weekday is Monday.
    const next = nextRun('0 8 * * 1-5', 'UTC', new Date('2026-10-09T18:00:00Z'));
    expect(next?.toISOString()).toBe('2026-10-12T08:00:00.000Z');
  });

  it('returns only instants strictly after the reference', () => {
    const next = nextRun('0 8 * * *', 'UTC', new Date('2026-10-05T08:00:00Z'));
    expect(next?.toISOString()).toBe('2026-10-06T08:00:00.000Z');
  });

  it('skips a time the clocks jump over', () => {
    // 01:30 does not exist in London on 2026-03-29; the next one is the day after.
    const next = nextRun('30 1 * * *', 'Europe/London', new Date('2026-03-28T12:00:00Z'));
    expect(next?.toISOString()).toBe('2026-03-30T00:30:00.000Z');
  });

  it('uses the first of a repeated time when the clocks go back', () => {
    // 01:30 happens twice in London on 2026-10-25; the first is still BST.
    const next = nextRun('30 1 * * *', 'Europe/London', new Date('2026-10-24T12:00:00Z'));
    expect(next?.toISOString()).toBe('2026-10-25T00:30:00.000Z');
  });

  it('handles a day of month', () => {
    const next = nextRun('0 9 15 * *', 'UTC', new Date('2026-10-20T00:00:00Z'));
    expect(next?.toISOString()).toBe('2026-11-15T09:00:00.000Z');
  });

  it('returns null for a date that never occurs', () => {
    expect(nextRun('0 0 31 2 *', 'UTC', new Date('2026-01-01T00:00:00Z'))).toBeNull();
  });
});

describe('nextRuns', () => {
  it('reports the earliest of each action, or null when there is none', () => {
    const after = new Date('2026-10-05T10:00:00Z');
    const runs = nextRuns(
      [
        { action: 'start', cron: '0 8 * * *', timezone: 'UTC' },
        { action: 'start', cron: '0 12 * * *', timezone: 'UTC' },
      ],
      after,
    );
    expect(runs).toEqual({ start: '2026-10-05T12:00:00.000Z', stop: null });
  });
});

describe('validateSchedules, malformed entries', () => {
  it('rejects an entry that is not an object and a cron that is not a string', () => {
    expect(() => validateSchedules(['0 8 * * *'])).toThrow(/schedule 1 must be an object/);
    expect(() => validateSchedules([null])).toThrow(/must be an object/);
    expect(() => validateSchedules([{ action: 'start', cron: 8 }])).toThrow(/cron must be a string/);
  });

  it('treats an empty time zone as UTC and rejects a non-string one', () => {
    expect(validateSchedules([{ action: 'stop', cron: '0 18 * * *', timezone: '' }])[0].timezone).toBe('UTC');
    expect(() => validateSchedules([{ action: 'stop', cron: '0 18 * * *', timezone: 5 }])).toThrow(
      /unknown time zone/,
    );
  });

  it('names the schedule that is wrong, by position', () => {
    expect(() =>
      validateSchedules([
        { action: 'start', cron: '0 8 * * *' },
        { action: 'stop', cron: '0 18 * * *', timezone: 'Nowhere/Land' },
      ]),
    ).toThrow(/schedule 2/);
  });
});

describe('parseCron, field edges', () => {
  it.each(['1,,2 * * * *', '1/2/3 * * * *', '*/x * * * *', '1- * * * *'])('rejects %j', (expr) => {
    expect(() => parseCron(expr)).toThrow(ScheduleError);
  });

  it('accepts a step on a start value, and reads the full day-of-month range as unrestricted', () => {
    expect(parseCron('5/20 * * * *').minutes).toEqual([5, 25, 45]);
    expect(parseCron('0 0 1-31 * 1').anyDayOfMonth).toBe(true);
    expect(toSchedulerCron('0 0 1-31 * 1')).toBe('cron(0 0 ? * MON *)');
  });
});

describe('identifiers', () => {
  it('derives a short, stable Scheduler name prefix per environment', () => {
    const prefix = schedulerNamePrefix('dev');
    expect(prefix).toBe(schedulerNamePrefix('dev'));
    expect(prefix).not.toBe(schedulerNamePrefix('prod'));
    expect(`${prefix}19`.length).toBeLessThanOrEqual(64);
    expect(schedulerNamePrefix('a'.repeat(64)).length).toBeLessThan(30);
  });

  it('recognises a scheduled run event', () => {
    expect(isScheduledRunEvent({ source: 'spinloop.schedule', action: 'start', environment: 'dev' })).toBe(true);
    expect(isScheduledRunEvent({ source: 'aws.events' })).toBe(false);
    expect(isScheduledRunEvent(null)).toBe(false);
  });
});
