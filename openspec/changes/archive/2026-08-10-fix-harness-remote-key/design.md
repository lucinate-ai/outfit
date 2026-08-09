## Context

See proposal.md — Why.

Two facts shape the approach. A remote endpoint's API key exists only in
Secrets Manager and is handed out by the env Lambda, so nothing on disk can
supply it; and no harness stores a secret itself — opencode substitutes
`{env:VAR}`, Pi resolves `$VAR`, lucinate reads `LUCINATE_OPENAI_API_KEY` —
so the key reaches the agent through the environment of the process outfit
launches, and nowhere else.

`outfit harness` therefore does two things that both need the key and did not
know about each other: it applies the Outfit (writing the harness config, and
deciding whether to warn that no key is set), and it builds the launched
agent's environment. The apply ran first, against `opencode.EnvResolver` — the
process environment and the `.env` beside the Outfit — neither of which can
hold a key the control plane has not been asked for yet.

## Goals / Non-Goals

**Goals:**

- The apply and the launch agree about which key will be in play, so a warning
  is only ever printed about a key that will genuinely be absent.
- A fetch that fails is visible, and stops the command when continuing could
  only produce an agent that cannot authenticate.
- `outfit remote env` stdout stays evaluable by a shell.

**Non-Goals:**

- Changing how the key reaches the agent. It is injected into the launched
  process's environment and still never written to a harness config.
- Caching or persisting the fetched key anywhere.
- Changing `outfit apply` or `outfit add`, which do not launch anything and so
  have no fetched key to reason about.

## Decisions

**Fetch before the apply, not after.** The alternative — keep the ordering and
teach `missingKeyWarning` to skip remote selections — suppresses the symptom
while leaving the apply resolving against an environment that is not the one
the agent will run with. Moving the fetch makes the two steps share a single
answer, and it is also what makes a fatal failure possible: nothing has been
written yet when the fetch fails, so the command can stop without leaving the
harness config half-updated.

**Widen the resolver rather than pass the key around.** `remoteLaunchResolver`
wraps the local lookup and answers the API key variable — only that variable,
and only where nothing local answers — from the fetched response. The apply,
the launch environment and lucinate's launch key all take the same function, so
they cannot disagree. Threading a `*remote.Response` into each of those call
sites instead would put the precedence rule (local wins) in three places.

An alternative was to inject the fetched key into outfit's own process
environment and let the existing lookups find it. Rejected: outfit does not
mutate its own environment on the launch path, deliberately, so that `ENV`
instructions and `.env` values shape only the child.

**Fatal only when no key is available.** The `harness-remote-env` spec asked
for a loud failure before launching, and the code silently swallowed the error
— so this had to be decided, not preserved. Always failing is wrong when the
user has already exported a key, or set one with `ENV`, since the fetch was
then only a convenience. Never failing is wrong when nothing supplies a key,
because the agent starts and collects 401s with no explanation. The condition
is therefore the availability of a key, not the fact of the failure.

`ENV` counts as a source, and is read straight from the parsed Outfit rather
than the process environment, because `overlayLocalEnv` applies it to the child
last and it overrides everything else there.

**The alias note moves to stderr, rather than being suppressed for one
command.** `readOutfit` is the single choke point every path-taking command
shares, and the note is prose about how the command resolved its argument, not
its result. Giving `remote env` a quiet mode would have left the same trap for
the next command whose stdout is consumed by something other than a human.

## Risks / Trade-offs

- A command that used to launch now sometimes refuses → the refusal is
  conditioned on there being no key at all, which is exactly the case where the
  launch could not have worked; the error names both remedies (start the
  endpoint, or export the key).
- The fetch is on the critical path of every remote launch → bounded by a 30s
  timeout, and announced on stderr so a pause is explained rather than
  mysterious. It previously had no timeout at all.
- Moving prose to stderr could hide it from someone piping stdout to a file →
  stderr is still on the terminal by default, which is where the note was
  actually being read.
- `remoteAPIKeyEnv` is matched by name, so the fetched key satisfies
  `OPENAI_API_KEY` and nothing else → the only providers a remote environment
  can deploy (`llamacpp`, `vllm`) both declare that variable, and
  `deployConfigFor` rejects the rest.
