import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import type { LambdaFunctionURLEvent } from 'aws-lambda';
import { schedulerNamePrefix, schedulesParam } from '../lambda/shared/schedules';

// The schedule Lambda: GET/PUT/DELETE on one environment's schedule list, kept
// in SSM and mirrored into EventBridge Scheduler schedules.

const LAMBDA_ENV = {
  SCHEDULE_GROUP: 'test-group',
  SCHEDULER_ROLE_ARN: 'arn:aws:iam::0:role/scheduler',
  START_FN_ARN: 'arn:aws:lambda:us-east-1:0:function:start',
  STOP_FN_ARN: 'arn:aws:lambda:us-east-1:0:function:stop',
};

const ssmSend = vi.fn();
const schedulerSend = vi.fn();

vi.mock('@aws-sdk/client-ssm', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@aws-sdk/client-ssm')>()),
  SSMClient: class {
    send = (cmd: unknown) => ssmSend(cmd);
  },
}));
vi.mock('@aws-sdk/client-scheduler', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@aws-sdk/client-scheduler')>()),
  SchedulerClient: class {
    send = (cmd: unknown) => schedulerSend(cmd);
  },
}));

let handler: (event: LambdaFunctionURLEvent) => Promise<{ statusCode: number; body: string }>;

beforeAll(async () => {
  Object.assign(process.env, LAMBDA_ENV);
  handler = (await import('../lambda/schedule/index')).handler as typeof handler;
});

function event(method: string, env: string | undefined, body?: unknown): LambdaFunctionURLEvent {
  return {
    queryStringParameters: env ? { env } : {},
    requestContext: { http: { method } },
    body: body === undefined ? undefined : JSON.stringify(body),
    isBase64Encoded: false,
  } as unknown as LambdaFunctionURLEvent;
}

/** The commands sent to a client, as [constructor name, input]. */
function sent(fn: typeof ssmSend): Array<[string, Record<string, any>]> {
  return fn.mock.calls.map(([cmd]) => [cmd.constructor.name, cmd.input]);
}

const OFFICE = [
  { action: 'start', cron: '0 8 * * 1-5', timezone: 'Europe/London' },
  { action: 'stop', cron: '0 18 * * 1-5', timezone: 'Europe/London' },
];

beforeEach(() => {
  vi.clearAllMocks();
  ssmSend.mockResolvedValue({});
  schedulerSend.mockImplementation(async (cmd: { constructor: { name: string } }) =>
    cmd.constructor.name === 'ListSchedulesCommand' ? { Schedules: [] } : {},
  );
});

describe('PUT', () => {
  it('stores the list and creates one schedule per entry', async () => {
    const res = await handler(event('PUT', 'dev', { schedules: OFFICE }));
    expect(res.statusCode).toBe(200);
    expect(JSON.parse(res.body).schedules).toEqual(OFFICE);

    const [put] = sent(ssmSend);
    expect(put[0]).toBe('PutParameterCommand');
    expect(put[1].Name).toBe(schedulesParam('dev'));
    expect(JSON.parse(put[1].Value)).toEqual(OFFICE);

    const creates = sent(schedulerSend).filter(([n]) => n === 'CreateScheduleCommand');
    expect(creates).toHaveLength(2);
    const prefix = schedulerNamePrefix('dev');
    expect(creates[0][1]).toMatchObject({
      Name: `${prefix}1`,
      GroupName: 'test-group',
      ScheduleExpression: 'cron(0 8 ? * MON,TUE,WED,THU,FRI *)',
      ScheduleExpressionTimezone: 'Europe/London',
      Target: { Arn: LAMBDA_ENV.START_FN_ARN, RoleArn: LAMBDA_ENV.SCHEDULER_ROLE_ARN },
    });
    expect(JSON.parse(creates[0][1].Target.Input)).toEqual({
      source: 'spinloop.schedule',
      action: 'start',
      environment: 'dev',
    });
    expect(creates[1][1].Target.Arn).toBe(LAMBDA_ENV.STOP_FN_ARN);
  });

  it('updates schedules that exist and deletes the ones no longer listed', async () => {
    const prefix = schedulerNamePrefix('dev');
    schedulerSend.mockImplementation(async (cmd: { constructor: { name: string } }) =>
      cmd.constructor.name === 'ListSchedulesCommand'
        ? { Schedules: [{ Name: `${prefix}1` }, { Name: `${prefix}2` }, { Name: `${prefix}3` }] }
        : {},
    );
    const res = await handler(event('PUT', 'dev', { schedules: [OFFICE[0]] }));
    expect(res.statusCode).toBe(200);

    const calls = sent(schedulerSend);
    expect(calls.filter(([n]) => n === 'UpdateScheduleCommand').map(([, i]) => i.Name)).toEqual([`${prefix}1`]);
    expect(calls.filter(([n]) => n === 'CreateScheduleCommand')).toHaveLength(0);
    expect(calls.filter(([n]) => n === 'DeleteScheduleCommand').map(([, i]) => i.Name)).toEqual([
      `${prefix}2`,
      `${prefix}3`,
    ]);
  });

  it('lists only the named environment’s schedules, so others are untouched', async () => {
    await handler(event('PUT', 'dev', { schedules: OFFICE }));
    const [list] = sent(schedulerSend);
    expect(list[0]).toBe('ListSchedulesCommand');
    expect(list[1].NamePrefix).toBe(schedulerNamePrefix('dev'));
    expect(list[1].NamePrefix).not.toBe(schedulerNamePrefix('prod'));
  });

  it('refuses an invalid expression and writes nothing', async () => {
    const res = await handler(
      event('PUT', 'dev', { schedules: [{ action: 'start', cron: 'every day' }] }),
    );
    expect(res.statusCode).toBe(400);
    expect(JSON.parse(res.body).error).toContain('"every day"');
    expect(ssmSend).not.toHaveBeenCalled();
    expect(schedulerSend).not.toHaveBeenCalled();
  });

  it('refuses a body that is not JSON', async () => {
    const bad = { ...event('PUT', 'dev'), body: '{nope' } as LambdaFunctionURLEvent;
    const res = await handler(bad);
    expect(res.statusCode).toBe(400);
    expect(ssmSend).not.toHaveBeenCalled();
  });

  it('treats an empty list as a clear', async () => {
    const res = await handler(event('PUT', 'dev', { schedules: [] }));
    expect(res.statusCode).toBe(200);
    expect(sent(ssmSend)[0][0]).toBe('DeleteParameterCommand');
  });
});

describe('DELETE', () => {
  it('removes the stored list and every schedule of the environment', async () => {
    const prefix = schedulerNamePrefix('dev');
    schedulerSend.mockImplementation(async (cmd: { constructor: { name: string } }) =>
      cmd.constructor.name === 'ListSchedulesCommand' ? { Schedules: [{ Name: `${prefix}1` }] } : {},
    );
    const res = await handler(event('DELETE', 'dev'));
    expect(res.statusCode).toBe(200);
    expect(JSON.parse(res.body).schedules).toEqual([]);
    expect(sent(ssmSend)[0][0]).toBe('DeleteParameterCommand');
    expect(sent(schedulerSend).filter(([n]) => n === 'DeleteScheduleCommand')).toHaveLength(1);
  });

  it('succeeds when nothing was stored', async () => {
    ssmSend.mockRejectedValue(Object.assign(new Error('missing'), { name: 'ParameterNotFound' }));
    const res = await handler(event('DELETE', 'dev'));
    expect(res.statusCode).toBe(200);
  });
});

describe('GET', () => {
  it('returns the stored list and the next run of each action', async () => {
    ssmSend.mockResolvedValue({ Parameter: { Value: JSON.stringify(OFFICE) } });
    const res = await handler(event('GET', 'dev'));
    expect(res.statusCode).toBe(200);
    const body = JSON.parse(res.body);
    expect(body.schedules).toEqual(OFFICE);
    expect(Date.parse(body.next.start)).toBeGreaterThan(Date.now());
    expect(Date.parse(body.next.stop)).toBeGreaterThan(Date.now());
  });

  it('returns an empty list with no next runs when nothing is stored', async () => {
    ssmSend.mockRejectedValue(Object.assign(new Error('missing'), { name: 'ParameterNotFound' }));
    const res = await handler(event('GET', 'dev'));
    expect(JSON.parse(res.body)).toMatchObject({ schedules: [], next: { start: null, stop: null } });
  });
});

describe('failures after validation', () => {
  it('keeps the stored list and surfaces the AWS error, rather than calling it a bad request', async () => {
    schedulerSend.mockRejectedValue(Object.assign(new Error('throttled'), { name: 'ThrottlingException' }));
    await expect(handler(event('PUT', 'dev', { schedules: OFFICE }))).rejects.toThrow('throttled');
    // The list was stored before the mirror was attempted, so a repeat converges.
    expect(sent(ssmSend)[0][0]).toBe('PutParameterCommand');
  });

  it('leaves the schedules alone when the stored list cannot be removed', async () => {
    ssmSend.mockRejectedValue(Object.assign(new Error('denied'), { name: 'AccessDeniedException' }));
    await expect(handler(event('DELETE', 'dev'))).rejects.toThrow('denied');
    expect(schedulerSend).not.toHaveBeenCalled();
  });

  it('removes schedules listed across several Scheduler pages', async () => {
    const prefix = schedulerNamePrefix('dev');
    let page = 0;
    schedulerSend.mockImplementation(async (cmd: { constructor: { name: string } }) => {
      if (cmd.constructor.name !== 'ListSchedulesCommand') return {};
      page += 1;
      return page === 1
        ? { Schedules: [{ Name: `${prefix}1` }], NextToken: 't' }
        : { Schedules: [{ Name: `${prefix}2` }] };
    });
    await handler(event('DELETE', 'dev'));
    expect(sent(schedulerSend).filter(([n]) => n === 'DeleteScheduleCommand')).toHaveLength(2);
  });
});

describe('request checks', () => {
  it('requires an environment', async () => {
    expect((await handler(event('GET', undefined))).statusCode).toBe(400);
  });

  it('rejects other methods', async () => {
    expect((await handler(event('POST', 'dev'))).statusCode).toBe(405);
  });
});
