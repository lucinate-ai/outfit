/**
 * The seed control surface: start, status, list, stop.
 *
 * Seeds are account-wide rather than per-environment — one model seeded once
 * serves every environment that names it — so nothing here takes an environment
 * name. What to seed arrives as a deploy-config-shaped body, and the weights
 * prefix is derived from it exactly as the deploy path derives it, so a seed and
 * a deploy for the same model always agree on where the weights go.
 */

import type { LambdaFunctionURLEvent, LambdaFunctionURLResult } from 'aws-lambda';
import {
  errorName,
  findManagedInstances,
  requireEnv,
  terminateInstance,
  type InstanceInfo,
} from '../shared/aws';
import { isRunner, weightsPrefixFor, type DeployConfig } from '../shared/deploy-config';
import { jsonResponse } from '../shared/http';
import { weightsPresent } from '../shared/seed';
import { buildSeedJob, launchSeedInstance, seedInfraFromEnv } from '../shared/seed/launch';
import {
  SEED_ID_TAG_KEY,
  SEED_TAG_VALUE,
  seedIdFor,
} from '../shared/seed/identity';
import { readSeedStatus, writeTerminalRecord } from '../shared/seed/status';

const TAG_KEY = requireEnv('TAG_KEY');
const MAX_CONCURRENT_SEEDS = Number(requireEnv('MAX_CONCURRENT_SEEDS'));

/** Live seed instances, optionally narrowed to one seed id. */
async function findSeeds(seedId?: string): Promise<InstanceInfo[]> {
  return findManagedInstances(
    TAG_KEY,
    SEED_TAG_VALUE,
    seedId ? [{ Name: `tag:${SEED_ID_TAG_KEY}`, Values: [seedId] }] : [],
  );
}

export async function handler(event: LambdaFunctionURLEvent): Promise<LambdaFunctionURLResult> {
  const method = event.requestContext?.http?.method ?? 'POST';
  const seedId = event.queryStringParameters?.id;

  try {
    if (method === 'GET') {
      return seedId ? await status(seedId) : await list();
    }
    if (method === 'DELETE') {
      if (!seedId) {
        return jsonResponse(400, { error: 'stopping a seed needs ?id=<seedId>' });
      }
      return await stop(seedId);
    }
    return await start(event);
  } catch (err) {
    console.log(JSON.stringify({ action: 'seed', method, error: errorName(err) }));
    return jsonResponse(502, { error: (err as Error).message });
  }
}

/**
 * POST — start a seed, or join the one already running for these weights.
 *
 * Convergence has two layers. This lookup handles the ordinary case of a repeat
 * request seconds or hours apart; the deterministic ClientToken inside
 * launchSeedInstance handles the case this lookup cannot, where two requests
 * pass it simultaneously.
 */
async function start(event: LambdaFunctionURLEvent): Promise<LambdaFunctionURLResult> {
  const raw =
    event.isBase64Encoded && event.body
      ? Buffer.from(event.body, 'base64').toString('utf8')
      : (event.body ?? '');

  let body: Record<string, unknown>;
  try {
    body = JSON.parse(raw || '{}') as Record<string, unknown>;
  } catch (err) {
    return jsonResponse(400, { error: `request is not valid JSON: ${(err as Error).message}` });
  }

  if (!isRunner(body.runner)) {
    return jsonResponse(400, { error: `runner must be vllm or llamacpp, got ${JSON.stringify(body.runner)}` });
  }
  const modelId = typeof body.modelId === 'string' ? body.modelId : '';
  if (!modelId) {
    return jsonResponse(400, { error: 'modelId must be a non-empty string' });
  }
  const quant = typeof body.quant === 'string' ? body.quant : '';
  const revision = typeof body.revision === 'string' ? body.revision : '';
  const force = body.force === true;

  const seedId = seedIdFor(body.runner, modelId, quant);
  const infra = seedInfraFromEnv();

  // Already in flight: join it. Reported as started:false so a repeated start
  // is unambiguous rather than looking like a fresh one.
  const running = await findSeeds(seedId);
  if (running.length > 0) {
    return jsonResponse(200, {
      seedId,
      instanceId: running[0].instanceId,
      started: false,
      joined: true,
      message: `a seed for ${modelId} is already running`,
    });
  }

  // Weights derive their location the same way the deploy path does, so a
  // caller never encodes the S3 layout and cannot ask for a different one.
  const cfg: DeployConfig = {
    runner: body.runner,
    modelId,
    quant,
    weightsPrefix: weightsPrefixFor(body.runner, modelId, quant),
    contextSize: 1,
    servedModelName: modelId,
    serveArgs: [],
  };

  if (!force && (await weightsPresent(infra.bucket, cfg))) {
    return jsonResponse(200, {
      seedId,
      started: false,
      alreadySeeded: true,
      message: `weights for ${modelId} are already in S3; pass force to seed them again`,
    });
  }

  // The cap bounds what a caller in a loop can launch. Counted after the
  // join above, so a repeat of an existing seed is never refused by it.
  const inFlight = await findSeeds();
  if (inFlight.length >= MAX_CONCURRENT_SEEDS) {
    return jsonResponse(429, {
      error: `${inFlight.length} seeds are already running (cap ${MAX_CONCURRENT_SEEDS}) — wait for one to finish`,
      seedId,
      running: inFlight.length,
      cap: MAX_CONCURRENT_SEEDS,
    });
  }

  const job = buildSeedJob(cfg, infra, revision);
  const launched = await launchSeedInstance(job, infra, { force });
  console.log(
    JSON.stringify({ action: 'seed-start', seedId, instanceId: launched.instanceId, modelId, force }),
  );
  return jsonResponse(200, {
    seedId,
    instanceId: launched.instanceId,
    started: true,
    joined: false,
    modelId,
    weightsPrefix: cfg.weightsPrefix,
    message: `seeding ${modelId} — follow it with \`outfit remote seed status ${seedId}\``,
  });
}

/** GET ?id= — one seed's state, whether or not its instance still exists. */
async function status(seedId: string): Promise<LambdaFunctionURLResult> {
  const instances = await findSeeds(seedId);
  const result = await readSeedStatus(seedId, instances[0] ?? null);
  // A seed nobody has ever run has neither an instance nor records; say so
  // rather than reporting it as a failure.
  if (!instances[0] && !result.lastReportAt) {
    return jsonResponse(404, { seedId, state: 'unknown', error: `no seed ${JSON.stringify(seedId)} is known` });
  }
  return jsonResponse(200, result);
}

/** GET — every seed in flight. */
async function list(): Promise<LambdaFunctionURLResult> {
  const instances = await findSeeds();
  const seeds = await Promise.all(
    instances.map(async (instance) => {
      const id = instance.tags?.[SEED_ID_TAG_KEY] ?? '';
      const detail = id ? await readSeedStatus(id, instance) : null;
      return {
        seedId: id,
        instanceId: instance.instanceId,
        modelId: instance.tags?.['cloud-vm-llm:seed-model'],
        state: detail?.state ?? 'starting',
        progressPercent: detail?.progressPercent,
        startedAt: instance.launchTime?.toISOString(),
        ageSeconds: instance.launchTime
          ? Math.round((Date.now() - instance.launchTime.getTime()) / 1000)
          : undefined,
      };
    }),
  );
  return jsonResponse(200, { seeds, count: seeds.length });
}

/**
 * DELETE ?id= — stop a seed.
 *
 * Stopping one that is not running is not an error: stopping twice must be
 * safe, and the caller's intent is satisfied either way.
 */
async function stop(seedId: string): Promise<LambdaFunctionURLResult> {
  const instances = await findSeeds(seedId);
  if (instances.length === 0) {
    return jsonResponse(200, { seedId, stopped: false, message: `no seed ${JSON.stringify(seedId)} is running` });
  }
  for (const instance of instances) {
    await terminateInstance(instance.instanceId);
    // Recorded as stopped, not left to be inferred as a crash by the join.
    await writeTerminalRecord(seedId, instance.instanceId, 'stopped', 'stopped by request');
  }
  console.log(JSON.stringify({ action: 'seed-stop', seedId, count: instances.length }));
  return jsonResponse(200, {
    seedId,
    stopped: true,
    instanceIds: instances.map((i) => i.instanceId),
    message: `stopped ${seedId}`,
  });
}
