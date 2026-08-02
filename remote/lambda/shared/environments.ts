/**
 * Environments: the per-instance layer on top of the shared stack. The shared
 * stack (deployed once by `outfit remote bootstrap`) holds the Lambdas, the
 * weights bucket, roles and the VPC; an environment is created by the deploy
 * Lambda on demand and owns everything specific to one endpoint — its Elastic
 * IP (stable address), its security group (per-env allowed CIDR), its API-key
 * secret, and its deploy-config/idle-state SSM parameters.
 *
 * Everything is keyed by the environment name, carried on resources as tags
 * (or encoded in names for the secret and SSM parameters), so one shared set
 * of Lambdas can find and drive every environment in the account.
 */

import {
  AllocateAddressCommand,
  AuthorizeSecurityGroupIngressCommand,
  CreateSecurityGroupCommand,
  DescribeAddressesCommand,
  DescribeSecurityGroupsCommand,
  EC2Client,
  RevokeSecurityGroupIngressCommand,
} from '@aws-sdk/client-ec2';
import {
  CreateSecretCommand,
  DescribeSecretCommand,
  GetSecretValueCommand,
  SecretsManagerClient,
} from '@aws-sdk/client-secrets-manager';
import { GetParameterCommand, PutParameterCommand, SSMClient } from '@aws-sdk/client-ssm';
import { randomBytes } from 'node:crypto';
import { errorName } from './aws';

const ec2 = new EC2Client({});
const secretsManager = new SecretsManagerClient({});
const ssm = new SSMClient({});

/** Tag naming which environment a resource (instance, EIP, SG) belongs to. */
export const ENV_TAG_KEY = 'cloud-vm-llm:env';

// A plain name: starts alphanumeric, then alphanumerics, dot, underscore or
// dash. Safe in an SSM parameter path, a Secrets Manager name, and a tag.
const ENV_NAME = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/;

export function isValidEnvironmentName(name: string): boolean {
  return ENV_NAME.test(name);
}

/**
 * The environment a Function URL event addresses: the `env` query parameter,
 * else a JSON body's `environment` field (already parsed by the caller).
 * Mandatory — there is deliberately no default here: a control API that
 * silently assumed an environment could create or drive the wrong instance.
 * Conveniences like a default environment belong in the CLI, not in AWS.
 * Throws on a missing or invalid name.
 */
export function environmentFrom(
  query: Record<string, string | undefined> | undefined | null,
  bodyEnvironment?: unknown,
): string {
  const name = query?.env ?? (typeof bodyEnvironment === 'string' ? bodyEnvironment : '');
  if (!name) {
    throw new Error('missing environment: pass ?env=<name> (or "environment" in the body)');
  }
  if (!isValidEnvironmentName(name)) {
    throw new Error(`invalid environment name ${JSON.stringify(name)}`);
  }
  return name;
}

/** SSM parameter holding what an environment serves. */
export function deployConfigParam(env: string): string {
  return `/cloud-vm-llm/${env}/deploy-config`;
}

/** SSM parameter holding an environment's idle-tracking state. */
export function idleStateParam(env: string): string {
  return `/cloud-vm-llm/${env}/idle-state`;
}

/** Secrets Manager name of an environment's API key. */
export function apiKeySecretName(env: string): string {
  return `cloud-vm-llm/${env}/api-key`;
}

/** An environment's base URL, from its Elastic IP. */
export function baseUrlFor(publicIp: string, port: number | string): string {
  return `http://${publicIp}:${port}/v1`;
}

export interface EnvEip {
  allocationId: string;
  publicIp: string;
}

/** Find an environment's Elastic IP by tag, if it has been allocated. */
export async function findEnvEip(env: string): Promise<EnvEip | null> {
  const result = await ec2.send(
    new DescribeAddressesCommand({
      Filters: [{ Name: `tag:${ENV_TAG_KEY}`, Values: [env] }],
    }),
  );
  const address = result.Addresses?.[0];
  if (!address?.AllocationId || !address.PublicIp) {
    return null;
  }
  return { allocationId: address.AllocationId, publicIp: address.PublicIp };
}

/** Allocate the environment's Elastic IP if it does not exist yet. */
export async function ensureEnvEip(env: string): Promise<EnvEip> {
  const existing = await findEnvEip(env);
  if (existing) {
    return existing;
  }
  const created = await ec2.send(
    new AllocateAddressCommand({
      Domain: 'vpc',
      TagSpecifications: [
        {
          ResourceType: 'elastic-ip',
          Tags: [
            { Key: ENV_TAG_KEY, Value: env },
            { Key: 'Name', Value: `cloud-vm-llm-${env}` },
          ],
        },
      ],
    }),
  );
  if (!created.AllocationId || !created.PublicIp) {
    throw new Error('AllocateAddress returned no allocation');
  }
  return { allocationId: created.AllocationId, publicIp: created.PublicIp };
}

/** Find an environment's security group by tag. */
export async function findEnvSecurityGroup(env: string): Promise<string | null> {
  const result = await ec2.send(
    new DescribeSecurityGroupsCommand({
      Filters: [{ Name: `tag:${ENV_TAG_KEY}`, Values: [env] }],
    }),
  );
  return result.SecurityGroups?.[0]?.GroupId ?? null;
}

/**
 * Ensure the environment's security group exists in the shared VPC and admits
 * exactly the allowed CIDR on the serving port. Ingress is per environment —
 * each environment scopes who can reach its own instance — so on a CIDR
 * change the old rules are revoked before the new one is authorised.
 */
export async function ensureEnvSecurityGroup(
  env: string,
  vpcId: string,
  port: number,
  allowedCidr: string,
): Promise<string> {
  let groupId = await findEnvSecurityGroup(env);
  if (!groupId) {
    const created = await ec2.send(
      new CreateSecurityGroupCommand({
        GroupName: `cloud-vm-llm-${env}`,
        Description: `cloud-vm-llm environment ${env} - ingress restricted to its allowed CIDR`,
        VpcId: vpcId,
        TagSpecifications: [
          {
            ResourceType: 'security-group',
            Tags: [
              { Key: ENV_TAG_KEY, Value: env },
              { Key: 'Name', Value: `cloud-vm-llm-${env}` },
            ],
          },
        ],
      }),
    );
    if (!created.GroupId) {
      throw new Error('CreateSecurityGroup returned no group id');
    }
    groupId = created.GroupId;
  }

  // Reconcile ingress to exactly [allowedCidr -> port].
  const described = await ec2.send(
    new DescribeSecurityGroupsCommand({ GroupIds: [groupId] }),
  );
  const current = described.SecurityGroups?.[0]?.IpPermissions ?? [];
  const wanted =
    current.length === 1 &&
    current[0].FromPort === port &&
    current[0].ToPort === port &&
    current[0].IpRanges?.length === 1 &&
    current[0].IpRanges[0].CidrIp === allowedCidr;
  if (wanted) {
    return groupId;
  }
  if (current.length > 0) {
    await ec2.send(
      new RevokeSecurityGroupIngressCommand({ GroupId: groupId, IpPermissions: current }),
    );
  }
  await ec2.send(
    new AuthorizeSecurityGroupIngressCommand({
      GroupId: groupId,
      IpPermissions: [
        {
          IpProtocol: 'tcp',
          FromPort: port,
          ToPort: port,
          IpRanges: [{ CidrIp: allowedCidr, Description: 'OpenAI-compatible API' }],
        },
      ],
    }),
  );
  return groupId;
}

/**
 * Ensure the environment's API-key secret exists, generating a value on first
 * creation. The key is per environment, so revoking or rotating one endpoint
 * never touches another.
 */
export async function ensureEnvApiKey(env: string): Promise<void> {
  const name = apiKeySecretName(env);
  try {
    await secretsManager.send(new DescribeSecretCommand({ SecretId: name }));
    return;
  } catch (err) {
    if (errorName(err) !== 'ResourceNotFoundException') {
      throw err;
    }
  }
  await secretsManager.send(
    new CreateSecretCommand({
      Name: name,
      Description: `API key for the cloud-vm-llm environment ${env}`,
      SecretString: randomBytes(36).toString('base64url'),
    }),
  );
}

/** Read an environment's API key. */
export async function readEnvApiKey(env: string): Promise<string> {
  const result = await secretsManager.send(
    new GetSecretValueCommand({ SecretId: apiKeySecretName(env) }),
  );
  return result.SecretString ?? '';
}

/** Ensure the environment's idle-state parameter exists (empty state). */
export async function ensureIdleState(env: string): Promise<void> {
  try {
    await ssm.send(new GetParameterCommand({ Name: idleStateParam(env) }));
  } catch (err) {
    if (errorName(err) !== 'ParameterNotFound') {
      throw err;
    }
    await ssm.send(
      new PutParameterCommand({
        Name: idleStateParam(env),
        Value: '{}',
        Type: 'String',
        Overwrite: false,
      }),
    );
  }
}
