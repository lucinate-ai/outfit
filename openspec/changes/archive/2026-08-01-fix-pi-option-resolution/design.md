## Context

`BuildProviderBlock` and `BuildPiProvider` translate the same catalogue entry into two
config formats. They necessarily differ in output shape, but they had also come to differ
in *input interpretation*, which is not a difference either format requires.

## Decisions

### Shared helpers rather than a parallel fix

The obvious fix — copy the `optionsFromEnv` loop into `BuildPiProvider` — would restore
correctness and leave the same trap for the next option kind. `optionsRequired` is the
proof: it was added to one builder only, months after `optionsFromEnv` was, by an author
with no reason to suspect the other builder existed as a separate code path.

So the resolution moves to `Provider.ResolveOptions` and `Provider.RequireOptions`, and
both builders call them. A third harness would inherit both for free.

### Where the per-provider variable sits in the precedence

`--base-url` > `OUTFIT_BASE_URL` > `optionsFromEnv` > `pi.baseUrl` > `options.baseURL`.

The interesting placement is `optionsFromEnv` above `pi.baseUrl`. Both are "where is the
server", but one is written by the catalogue and one by the user, and a user who exports
`OMLX_BASE_URL` has said something specific about their machine that a shipped default
cannot know. The two cannot actually collide today — every provider with an
`optionsFromEnv.baseURL` falls through to `options.baseURL`, and the only provider with a
`pi.baseUrl` (`openrouter`) has no endpoint variable — so this ordering is about what
happens when someone adds one, not about current behaviour.

The alternative, slotting it below `pi.baseUrl`, has a nastier failure mode: adding a
`pi.baseUrl` to `omlx` later would silently stop `OMLX_BASE_URL` from working, which is
the same class of bug being fixed here.

### Failing on a required option Pi cannot express

A Pi provider entry is `{baseUrl, api, apiKey, models}`. There is nowhere to put a Vertex
`project`, so a provider that needs one genuinely cannot be served by Pi.

Two guards, because they cover different things. The runtime check in `BuildPiProvider`
catches a **runtime** catalogue (`--providers`/`OUTFIT_PROVIDERS`), which no test can see.
The integrity test catches the **embedded** catalogue, and states the invariant somewhere
a reader will find it. Without the integrity test the runtime error would be the only
documentation; without the runtime check the invariant would hold only for the shipped
file.

## Risks / Trade-offs

- **Users relying on the broken behaviour**: implausible. The prior behaviour was a Pi
  config pointing at a server the user had explicitly redirected away from, which fails at
  first use rather than working differently.
- **Ordering choice is forward-looking**: `optionsFromEnv` above `pi.baseUrl` changes
  nothing today and is asserted by a precedence test, so a future provider combining both
  gets the documented behaviour rather than an accident.
