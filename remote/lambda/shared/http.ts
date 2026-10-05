import type { LambdaFunctionURLResult } from 'aws-lambda';

// Names the spinloop version this control plane was deployed with. The CLI
// compares it against its own version on every call, so it rides on every
// response — errors included — rather than costing a separate request.
export const CONTROL_PLANE_VERSION_HEADER = 'x-spinloop-control-plane-version';

export function jsonResponse(
  statusCode: number,
  body: unknown,
  extraHeaders: Record<string, string> = {},
): LambdaFunctionURLResult {
  return {
    statusCode,
    headers: {
      'content-type': 'application/json',
      [CONTROL_PLANE_VERSION_HEADER]: process.env.CONTROL_PLANE_VERSION || 'dev',
      ...extraHeaders,
    },
    body: JSON.stringify(body),
  };
}
