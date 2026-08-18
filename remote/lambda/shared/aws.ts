import {
  AssociateAddressCommand,
  CreateTagsCommand,
  DescribeImagesCommand,
  DescribeInstancesCommand,
  EC2Client,
  type Filter,
  RunInstancesCommand,
  type RunInstancesCommandInput,
  StartInstancesCommand,
  StopInstancesCommand,
  TerminateInstancesCommand,
  _InstanceType,
} from '@aws-sdk/client-ec2';
import {
  DescribeInstanceInformationCommand,
  GetCommandInvocationCommand,
  GetParameterCommand,
  PutParameterCommand,
  SSMClient,
  SendCommandCommand,
} from '@aws-sdk/client-ssm';
import { randomUUID } from 'node:crypto';
import { type DeployConfig, parseDeployConfig } from './deploy-config';
import { DAEMON_STOP_CMD } from './daemon';

const ec2 = new EC2Client({});
const ssm = new SSMClient({});

export function requireEnv(name: string): string {
  const value = process.env[name];
  if (!value) {
    throw new Error(`Missing required environment variable ${name}`);
  }
  return value;
}

export function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export function errorName(err: unknown): string {
  return err instanceof Error ? err.name : String(err);
}

/**
 * Instance tag holding an ISO-8601 UTC datetime before which automatic
 * termination (idle check and the max-runtime cap) must not fire — a manual
 * "keep this alive until" override, e.g. while debugging on the box. A manual
 * `outfit remote stop` still terminates it: the tag guards against accidental
 * death, not deliberate shutdown.
 */
export const RETAIN_UNTIL_TAG = 'Retain-Until';

/**
 * Instance tag holding the ISO-8601 UTC time the control plane last issued a
 * stop for this instance (idle sweep or a manual `outfit remote pause`).
 * Stopped instances are billed for storage while they sleep, so the sweep
 * counts stop retention from this tag; EC2 itself exposes no stop time.
 */
export const STOPPED_AT_TAG = 'Stopped-At';

/**
 * Instance tag holding the ISO-8601 UTC time the control plane last re-woke a
 * stopped instance. The max-runtime and grace-period judgements measure the
 * current session, so start writes this rather than trusting how `LaunchTime`
 * survives a stop/start cycle.
 */
export const STARTED_AT_TAG = 'Started-At';

function parseTagDate(tags: { Key?: string; Value?: string }[] | undefined, key: string): Date | undefined {
  const raw = tags?.find((t) => t.Key === key)?.Value;
  if (!raw) {
    return undefined;
  }
  const ms = Date.parse(raw);
  return Number.isNaN(ms) ? undefined : new Date(ms);
}

function parseRetainUntil(tags?: { Key?: string; Value?: string }[]): Date | undefined {
  return parseTagDate(tags, RETAIN_UNTIL_TAG);
}

export interface InstanceInfo {
  instanceId: string;
  state: string;
  /** EC2 instance type (e.g. g6e.xlarge), for the stats reply and cost estimation. */
  instanceType?: string;
  launchTime?: Date;
  /** Parsed from the Retain-Until tag, if present and a valid datetime. */
  retainUntil?: Date;
  /** Parsed from the Stopped-At tag — when the stop was issued, if ever. */
  stoppedAt?: Date;
  /** Parsed from the Started-At tag — when the current session began, if the instance was re-woken. */
  startedAt?: Date;
  /** The environment the instance belongs to (its cloud-vm-llm:env tag). */
  environment?: string;
}

/** Describe a specific instance by id (used to poll a just-launched one). */
export async function getInstance(instanceId: string): Promise<InstanceInfo> {
  const result = await ec2.send(new DescribeInstancesCommand({ InstanceIds: [instanceId] }));
  const instance = result.Reservations?.[0]?.Instances?.[0];
  if (!instance) {
    throw new Error(`Instance ${instanceId} not found`);
  }
  return {
    instanceId,
    state: instance.State?.Name ?? 'unknown',
    launchTime: instance.LaunchTime,
    retainUntil: parseRetainUntil(instance.Tags),
    stoppedAt: parseTagDate(instance.Tags, STOPPED_AT_TAG),
    startedAt: parseTagDate(instance.Tags, STARTED_AT_TAG),
  };
}

/**
 * Find live managed instances by tag. An environment holds no fixed instance
 * id — the start Lambda launches one and the stop Lambda terminates it — so
 * everything discovers instances this way. Pass extra tag filters (e.g. the
 * environment tag) to narrow to one environment; without them this returns
 * every environment's instance, which is how the idle sweep covers them all.
 */
export async function findManagedInstances(
  tagKey: string,
  tagValue: string,
  extraFilters: Filter[] = [],
): Promise<InstanceInfo[]> {
  const result = await ec2.send(
    new DescribeInstancesCommand({
      Filters: [
        { Name: `tag:${tagKey}`, Values: [tagValue] },
        // stopped stays in: the sweep must see stopped instances to terminate
        // them after retention, and the start Lambda must find the one to
        // re-wake. stopping/shutting-down are transient — no body of code acts
        // on them, only waits them out.
        { Name: 'instance-state-name', Values: ['pending', 'running', 'stopped'] },
        ...extraFilters,
      ],
    }),
  );
  const instances = result.Reservations?.flatMap((r) => r.Instances ?? []) ?? [];
  return instances
    .filter((i) => i.InstanceId)
    .map((instance) => ({
      instanceId: instance.InstanceId!,
      state: instance.State?.Name ?? 'unknown',
      instanceType: instance.InstanceType as string | undefined,
      launchTime: instance.LaunchTime,
      retainUntil: parseRetainUntil(instance.Tags),
      stoppedAt: parseTagDate(instance.Tags, STOPPED_AT_TAG),
      startedAt: parseTagDate(instance.Tags, STARTED_AT_TAG),
      environment: instance.Tags?.find((t) => t.Key === 'cloud-vm-llm:env')?.Value,
    }));
}

/** Find one environment's live managed instance, if any. */
export async function findManagedInstance(
  tagKey: string,
  tagValue: string,
  extraFilters: Filter[] = [],
): Promise<InstanceInfo | null> {
  const instances = await findManagedInstances(tagKey, tagValue, extraFilters);
  return instances[0] ?? null;
}

/**
 * Find the newest available AMI matching the given filters (owned by this
 * account). This is how the runtime discovers the baked image — the image
 * pipeline tags each AMI, and we pick the most recently created. Returns null
 * if none match (e.g. no bake has succeeded yet).
 */
export async function findLatestAmi(filters: Filter[]): Promise<string | null> {
  const result = await ec2.send(new DescribeImagesCommand({ Owners: ['self'], Filters: filters }));
  const images = (result.Images ?? []).filter((i) => i.State === 'available' && i.ImageId);
  if (images.length === 0) {
    return null;
  }
  // CreationDate is ISO 8601, so a lexicographic sort is chronological.
  images.sort((a, b) => (b.CreationDate ?? '').localeCompare(a.CreationDate ?? ''));
  return images[0].ImageId ?? null;
}

export interface LaunchSpec {
  imageId: string;
  instanceType: string;
  subnetId: string;
  securityGroupId: string;
  instanceProfileArn: string;
  userData: string; // plain text; encoded here
  tags: Record<string, string>;
  /**
   * Terminate (rather than stop) when the guest shuts itself down — how the
   * seed instance disposes of itself once the weights are in S3.
   */
  terminateOnShutdown?: boolean;
}

/**
 * Launch one instance in the given subnet. Throws on failure — the caller
 * catches capacity/unsupported errors and retries in another AZ's subnet.
 * Returns the new instance id.
 */
export async function runInstance(spec: LaunchSpec): Promise<string> {
  const input: RunInstancesCommandInput = {
    ImageId: spec.imageId,
    InstanceType: spec.instanceType as _InstanceType,
    MinCount: 1,
    MaxCount: 1,
    // Idempotency: RunInstances is not idempotent, and the SDK auto-retries on
    // transient errors. Without a token, a retry of a call that actually
    // launched (but whose response was lost) launches a *second* instance — or
    // hits the vCPU limit the first one just consumed. The token makes a retry
    // return the same instance instead.
    ClientToken: randomUUID(),
    SubnetId: spec.subnetId,
    SecurityGroupIds: [spec.securityGroupId],
    IamInstanceProfile: { Arn: spec.instanceProfileArn },
    UserData: Buffer.from(spec.userData).toString('base64'),
    MetadataOptions: { HttpTokens: 'required' },
    ...(spec.terminateOnShutdown ? { InstanceInitiatedShutdownBehavior: 'terminate' as const } : {}),
    TagSpecifications: [
      {
        ResourceType: 'instance',
        Tags: Object.entries(spec.tags).map(([Key, Value]) => ({ Key, Value })),
      },
    ],
  };
  const result = await ec2.send(new RunInstancesCommand(input));
  const instanceId = result.Instances?.[0]?.InstanceId;
  if (!instanceId) {
    throw new Error('RunInstances returned no instance id');
  }
  return instanceId;
}

/** Errors that mean "this AZ can't take the instance" — try the next subnet. */
export function isCapacityError(err: unknown): boolean {
  return ['InsufficientInstanceCapacity', 'Unsupported', 'InstanceLimitExceeded'].includes(
    errorName(err),
  );
}

export async function terminateInstance(instanceId: string): Promise<void> {
  await ec2.send(new TerminateInstancesCommand({ InstanceIds: [instanceId] }));
}

export async function stopInstance(instanceId: string): Promise<void> {
  await ec2.send(new StopInstancesCommand({ InstanceIds: [instanceId] }));
}

export async function startInstance(instanceId: string): Promise<void> {
  await ec2.send(new StartInstancesCommand({ InstanceIds: [instanceId] }));
}

/** Set (or replace) one instance tag. Best-effort callers may let this throw. */
export async function tagInstance(instanceId: string, key: string, value: string): Promise<void> {
  await ec2.send(
    new CreateTagsCommand({
      Resources: [instanceId],
      Tags: [{ Key: key, Value: value }],
    }),
  );
}

export async function associateEip(allocationId: string, instanceId: string): Promise<void> {
  await ec2.send(
    new AssociateAddressCommand({
      AllocationId: allocationId,
      InstanceId: instanceId,
      AllowReassociation: true,
    }),
  );
}

export async function isSsmAgentOnline(instanceId: string): Promise<boolean> {
  const result = await ssm.send(
    new DescribeInstanceInformationCommand({
      Filters: [{ Key: 'InstanceIds', Values: [instanceId] }],
    }),
  );
  return result.InstanceInformationList?.[0]?.PingStatus === 'Online';
}

export interface CommandResult {
  status: string;
  stdout: string;
}

/**
 * Run a shell command on the instance via SSM Run Command and wait for the
 * result — how the Lambdas reach vLLM on localhost without living in the VPC.
 * GetCommandInvocation truncates stdout at 24 000 chars, so commands must
 * filter their own output (e.g. grep /metrics on-instance).
 */
export async function runShellCommand(
  instanceId: string,
  command: string,
  timeoutSeconds = 30,
): Promise<CommandResult> {
  const sent = await ssm.send(
    new SendCommandCommand({
      DocumentName: 'AWS-RunShellScript',
      InstanceIds: [instanceId],
      Parameters: { commands: [command], executionTimeout: [String(timeoutSeconds)] },
    }),
  );
  const commandId = sent.Command?.CommandId;
  if (!commandId) {
    throw new Error('SendCommand returned no command id');
  }

  const deadline = Date.now() + (timeoutSeconds + 30) * 1000;
  while (Date.now() < deadline) {
    await sleep(1500);
    try {
      const invocation = await ssm.send(
        new GetCommandInvocationCommand({ CommandId: commandId, InstanceId: instanceId }),
      );
      const status = invocation.Status ?? 'Unknown';
      if (status !== 'Pending' && status !== 'InProgress' && status !== 'Delayed') {
        return { status, stdout: (invocation.StandardOutputContent ?? '').trim() };
      }
    } catch (err) {
      // The invocation record is eventually consistent after SendCommand.
      if (errorName(err) !== 'InvocationDoesNotExist') {
        throw err;
      }
    }
  }
  return { status: 'Timeout', stdout: '' };
}

/**
 * Ask the daemon on an instance to stop its engine, best-effort. Returns true
 * when the daemon answered with a success status, false when it did not answer
 * or the command failed. This is deliberately tolerant: a crashed daemon should
 * not prevent the Lambda from proceeding to stop the EC2 instance.
 */
export async function stopEngineDaemon(instanceId: string): Promise<boolean> {
  try {
    const result = await runShellCommand(instanceId, DAEMON_STOP_CMD, 10);
    return result.status === 'Success';
  } catch {
    return false;
  }
}

/**
 * Read the deploy-config (what to serve) from its SSM parameter. Throws via
 * parseDeployConfig if unset/invalid — the start Lambda surfaces that as a
 * clear "run outfit remote deploy" rather than launching a mis-configured box.
 */
export async function readDeployConfig(paramName: string): Promise<DeployConfig> {
  const result = await ssm.send(new GetParameterCommand({ Name: paramName }));
  return parseDeployConfig(result.Parameter?.Value);
}

/** Overwrite the deploy-config parameter — the deploy Lambda's one mutation. */
export async function writeDeployConfig(paramName: string, config: DeployConfig): Promise<void> {
  await ssm.send(
    new PutParameterCommand({
      Name: paramName,
      Value: JSON.stringify(config),
      Type: 'String',
      Overwrite: true,
    }),
  );
}
