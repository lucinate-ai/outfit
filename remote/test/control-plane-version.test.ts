import * as os from 'os';
import * as path from 'path';
import * as cdk from 'aws-cdk-lib';
import { Template } from 'aws-cdk-lib/assertions';
import { afterEach, describe, expect, it } from 'vitest';
import { loadConfig } from '../lib/config';
import { LlmStack } from '../lib/llm-stack';
import { CONTROL_PLANE_VERSION_HEADER, jsonResponse } from '../lambda/shared/http';

const NO_DOTENV = path.join(os.tmpdir(), 'cloud-vm-llm-no-such-env');

// jsonResponse always builds the structured form of the Lambda result.
function headersOf(res: ReturnType<typeof jsonResponse>): Record<string, string> {
  return (res as { headers: Record<string, string> }).headers;
}

describe('control plane version header', () => {
  const original = process.env.CONTROL_PLANE_VERSION;
  afterEach(() => {
    if (original === undefined) {
      delete process.env.CONTROL_PLANE_VERSION;
    } else {
      process.env.CONTROL_PLANE_VERSION = original;
    }
  });

  it('is named x-spinloop-control-plane-version', () => {
    expect(CONTROL_PLANE_VERSION_HEADER).toBe('x-spinloop-control-plane-version');
  });

  it('carries the deployed version on a success response', () => {
    process.env.CONTROL_PLANE_VERSION = '1.30.0';
    const res = jsonResponse(200, { ok: true });
    expect(headersOf(res)[CONTROL_PLANE_VERSION_HEADER]).toBe('1.30.0');
    expect(headersOf(res)['content-type']).toBe('application/json');
  });

  it('carries the deployed version on an error response', () => {
    process.env.CONTROL_PLANE_VERSION = '1.30.0';
    expect(headersOf(jsonResponse(503, { message: 'x' }))[CONTROL_PLANE_VERSION_HEADER]).toBe('1.30.0');
  });

  it('falls back to dev when no version was deployed', () => {
    delete process.env.CONTROL_PLANE_VERSION;
    expect(headersOf(jsonResponse(200, {}))[CONTROL_PLANE_VERSION_HEADER]).toBe('dev');
  });
});

describe('control plane version config', () => {
  const original = process.env.SPINLOOP_CONTROL_PLANE_VERSION;
  afterEach(() => {
    if (original === undefined) {
      delete process.env.SPINLOOP_CONTROL_PLANE_VERSION;
    } else {
      process.env.SPINLOOP_CONTROL_PLANE_VERSION = original;
    }
  });

  it('defaults to dev', () => {
    delete process.env.SPINLOOP_CONTROL_PLANE_VERSION;
    expect(loadConfig(new cdk.App(), NO_DOTENV).controlPlaneVersion).toBe('dev');
  });

  it('reads SPINLOOP_CONTROL_PLANE_VERSION', () => {
    process.env.SPINLOOP_CONTROL_PLANE_VERSION = '1.30.0';
    expect(loadConfig(new cdk.App(), NO_DOTENV).controlPlaneVersion).toBe('1.30.0');
  });

  it('lets the controlPlaneVersion context value win', () => {
    process.env.SPINLOOP_CONTROL_PLANE_VERSION = '1.30.0';
    const app = new cdk.App({ context: { controlPlaneVersion: '2.0.0' } });
    expect(loadConfig(app, NO_DOTENV).controlPlaneVersion).toBe('2.0.0');
  });

  it('sets CONTROL_PLANE_VERSION on every Lambda', () => {
    const app = new cdk.App({ context: { controlPlaneVersion: '1.30.0' } });
    const config = loadConfig(app, NO_DOTENV);
    const template = Template.fromStack(
      new LlmStack(app, 'test-runtime', { config, env: { region: config.region } }),
    );
    const fns = Object.values(template.findResources('AWS::Lambda::Function')) as any[];
    expect(fns).toHaveLength(8);
    for (const fn of fns) {
      expect(fn.Properties.Environment.Variables.CONTROL_PLANE_VERSION).toBe('1.30.0');
    }
  });
});
