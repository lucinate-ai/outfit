## 1. The collector

- [x] 1.1 Give `engineSpec` a per-family request-counter name:
      `request_success_total` for vllm, none for llamacpp (design D1).
      Verify the existing vllm parse test still reads the figure, and a
      llamacpp parse of a real engine's lines yields none.
- [x] 1.2 Make `TokenStats.Requests` a `*int` with
      `json:"requests,omitempty"`; set it from the family's counter where the
      family names one, leave it unset otherwise (design D2). Verify a
      genuine zero from an engine that serves the counter still serialises as
      `"requests": 0`, and the field is absent from the serialised statistics
      where the family names none.
- [x] 1.3 Replace the fabricated `llamacpp:request_success_total` line in the
      unit-test fixture with the lines a real llama.cpp engine serves
      (design D4), and assert the parse carries no request figure.

## 2. The renderers and the contract

- [x] 2.1 Draw the `requests:` line in the shared token block only where the
      figure is present (design D3). Verify every surface that draws the
      block — the dashboard tile, the `metrics` bar and table formats, the
      serve view — keeps the line for statistics that carry the figure and
      drops it for statistics that do not.
- [x] 2.2 Mark the field optional in `docs/openapi.yaml`, with a description
      saying which engines set it, and in the control plane's TypeScript
      mirror (`remote/lambda/shared/stats.ts`). Verify the OpenAPI suite still
      passes — it compares field names, so the field stays described; its
      optionality is the description's.

## 3. Fixtures and tests

- [x] 3.1 Drop the fabricated line from
      `examples/fleet-docker/engine/engine-config.yaml` and
      `examples/gateway-docker/engine/engine-config.yaml`. Verify neither
      example engine serves a line a real engine of that family would not
      serve, and the fleet-docker-example spec's scenario — the reported
      counters are those the engine's metrics endpoint served — still holds.
- [x] 3.2 Update every test that constructs `TokenStats` with the plain
      `Requests:` field, across `cmd/spinloop`, `internal/remote` and
      `internal/fleet`. Verify `go test ./...` is green.

## 4. Verification

- [x] 4.1 Run `gofmt -l .` (expect no output), `go vet ./...` and
      `go test ./... -cover`, confirming total coverage is still >= 80%.
- [x] 4.2 Against a real llama.cpp engine run with `--metrics`, verify the
      daemon's statistics reply carries no `requests` field, and the tile and
      the `metrics` formats draw no `requests:` line for it.
