import {
  CreateScheduleCommand,
  DeleteScheduleCommand,
  ListSchedulesCommand,
  SchedulerClient,
  UpdateScheduleCommand,
} from '@aws-sdk/client-scheduler';
import {
  DeleteParameterCommand,
  GetParameterCommand,
  PutParameterCommand,
  SSMClient,
} from '@aws-sdk/client-ssm';
import type { LambdaFunctionURLEvent, LambdaFunctionURLResult } from 'aws-lambda';
import { errorName, requireEnv } from '../shared/aws';
import { environmentFrom } from '../shared/environments';
import { jsonResponse } from '../shared/http';
import {
  nextRuns,
  SCHEDULE_EVENT_SOURCE,
  ScheduleError,
  schedulerNamePrefix,
  schedulesParam,
  toSchedulerCron,
  validateSchedules,
  type Schedule,
  type ScheduledRunEvent,
} from '../shared/schedules';

const SCHEDULE_GROUP = requireEnv('SCHEDULE_GROUP');
const SCHEDULER_ROLE_ARN = requireEnv('SCHEDULER_ROLE_ARN');
const START_FN_ARN = requireEnv('START_FN_ARN');
const STOP_FN_ARN = requireEnv('STOP_FN_ARN');

const ssm = new SSMClient({});
const scheduler = new SchedulerClient({});

/**
 * Function URL for an environment's schedules, named by `?env=`:
 * - GET reads the list and when each action next fires.
 * - PUT replaces the list with the JSON body `{"schedules": [...]}`.
 * - DELETE removes every schedule.
 *
 * The list is kept in SSM and mirrored into EventBridge Scheduler schedules.
 * Input is validated before anything is written, so a rejected request leaves
 * the existing schedules as they were.
 */
export async function handler(event: LambdaFunctionURLEvent): Promise<LambdaFunctionURLResult> {
  let env: string;
  try {
    env = environmentFrom(event.queryStringParameters);
  } catch (err) {
    return jsonResponse(400, { error: (err as Error).message });
  }
  const method = event.requestContext?.http?.method ?? 'GET';
  try {
    switch (method) {
      case 'GET':
        return report(env, await readSchedules(env));
      case 'PUT':
        return await replace(env, event);
      case 'DELETE':
        return await clear(env);
      default:
        return jsonResponse(405, { error: `unsupported method ${method}; accepted: GET, PUT, DELETE` });
    }
  } catch (err) {
    if (err instanceof ScheduleError) {
      return jsonResponse(400, { error: err.message });
    }
    console.log(JSON.stringify({ cmd: 'schedule', method, environment: env, error: errorName(err) }));
    throw err;
  }
}

function report(env: string, schedules: Schedule[]): LambdaFunctionURLResult {
  return jsonResponse(200, {
    environment: env,
    schedules,
    next: nextRuns(schedules, new Date()),
  });
}

async function replace(env: string, event: LambdaFunctionURLEvent): Promise<LambdaFunctionURLResult> {
  let body: unknown;
  try {
    body = JSON.parse(event.isBase64Encoded ? Buffer.from(event.body ?? '', 'base64').toString() : (event.body ?? ''));
  } catch {
    throw new ScheduleError('the body must be JSON: {"schedules": [...]}');
  }
  const schedules = validateSchedules((body as { schedules?: unknown } | null)?.schedules);
  // Everything that can be refused is refused above. Past here a failure is the
  // AWS side, and the stored list is already the intended one, so repeating the
  // request converges.
  const expressions = schedules.map((s) => toSchedulerCron(s.cron));
  if (schedules.length === 0) {
    return clear(env);
  }
  await ssm.send(
    new PutParameterCommand({
      Name: schedulesParam(env),
      Value: JSON.stringify(schedules),
      Type: 'String',
      Overwrite: true,
    }),
  );
  await reconcile(env, schedules, expressions);
  console.log(JSON.stringify({ cmd: 'schedule-set', environment: env, count: schedules.length }));
  return report(env, schedules);
}

async function clear(env: string): Promise<LambdaFunctionURLResult> {
  try {
    await ssm.send(new DeleteParameterCommand({ Name: schedulesParam(env) }));
  } catch (err) {
    if (errorName(err) !== 'ParameterNotFound') {
      throw err;
    }
  }
  await reconcile(env, [], []);
  console.log(JSON.stringify({ cmd: 'schedule-clear', environment: env }));
  return report(env, []);
}

async function readSchedules(env: string): Promise<Schedule[]> {
  try {
    const out = await ssm.send(new GetParameterCommand({ Name: schedulesParam(env) }));
    return validateSchedules(JSON.parse(out.Parameter?.Value ?? '[]'));
  } catch (err) {
    if (errorName(err) === 'ParameterNotFound') {
      return [];
    }
    throw err;
  }
}

/** Make the environment's Scheduler schedules in the group match the list. */
async function reconcile(env: string, schedules: Schedule[], expressions: string[]): Promise<void> {
  const prefix = schedulerNamePrefix(env);
  const existing = new Set<string>();
  let token: string | undefined;
  do {
    const page = await scheduler.send(
      new ListSchedulesCommand({ GroupName: SCHEDULE_GROUP, NamePrefix: prefix, NextToken: token }),
    );
    for (const s of page.Schedules ?? []) {
      if (s.Name) {
        existing.add(s.Name);
      }
    }
    token = page.NextToken;
  } while (token);

  const wanted = new Set<string>();
  for (const [i, schedule] of schedules.entries()) {
    const name = `${prefix}${i + 1}`;
    wanted.add(name);
    const input: ScheduledRunEvent = {
      source: SCHEDULE_EVENT_SOURCE,
      action: schedule.action,
      environment: env,
    };
    const params = {
      Name: name,
      GroupName: SCHEDULE_GROUP,
      ScheduleExpression: expressions[i],
      ScheduleExpressionTimezone: schedule.timezone,
      FlexibleTimeWindow: { Mode: 'OFF' as const },
      Target: {
        Arn: schedule.action === 'start' ? START_FN_ARN : STOP_FN_ARN,
        RoleArn: SCHEDULER_ROLE_ARN,
        Input: JSON.stringify(input),
      },
    };
    await scheduler.send(existing.has(name) ? new UpdateScheduleCommand(params) : new CreateScheduleCommand(params));
  }
  for (const name of existing) {
    if (!wanted.has(name)) {
      await scheduler.send(new DeleteScheduleCommand({ Name: name, GroupName: SCHEDULE_GROUP }));
    }
  }
}
