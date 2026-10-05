## 1. Control plane

- [x] 1.1 Add `controlPlaneVersion` to `remote/lib/config.ts` (context key, env var `SPINLOOP_CONTROL_PLANE_VERSION`, default `dev`)
- [x] 1.2 Set `CONTROL_PLANE_VERSION` in `commonEnv` for every Lambda in `remote/lib/llm-stack.ts`
- [x] 1.3 Add `x-spinloop-control-plane-version` to every response in `jsonResponse` (`remote/lambda/shared/http.ts`)
- [x] 1.4 Add vitest cases: header on success and error responses, `dev` default, stack passes the env var

## 2. CLI

- [x] 2.1 Return response headers from `send` and `callStats` and compare the version header against the CLI version (trim `v`, skip when absent, empty or `dev`)
- [x] 2.2 Print the warning once per process to stderr, naming both versions and `spinloop remote bootstrap`
- [x] 2.3 Pass the CLI version to the bootstrap deploy as `SPINLOOP_CONTROL_PLANE_VERSION`
- [x] 2.4 Add Go tests: mismatch, match, `v` prefix, absent header, dev on either side, once per process, stdout untouched

## 3. Docs

- [x] 3.1 Document the warning in the `spinloop remote` docs and the `remote/` README
