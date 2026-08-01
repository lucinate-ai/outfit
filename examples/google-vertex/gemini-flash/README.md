# Gemini 2.0 Flash on GCP Vertex AI

Point opencode at Google's Gemini running on
[GCP Vertex AI](https://cloud.google.com/vertex-ai), using the [`Outfit`](Outfit)
in this directory. Like the Claude-on-Vertex example, this authenticates with
your **Google credentials** (Application Default Credentials) — no API key — and
needs a **project**. The difference is the provider (`google-vertex` serves
Gemini) and the model id.

## Prerequisites

- A Google Cloud project with the **Vertex AI API** enabled.
- Application Default Credentials the SDK can find, via either:
  - `gcloud auth application-default login` (interactive), or
  - a service-account key file pointed to by `GOOGLE_APPLICATION_CREDENTIALS`.
- IAM permission to call the model (e.g. the *Vertex AI User* role).

## 1. Set the project (and, optionally, the location)

```sh
export GOOGLE_VERTEX_PROJECT=my-gcp-project
export GOOGLE_VERTEX_LOCATION=us-central1   # optional; defaults to `global`
```

The project is required; `outfit apply` fails with a clear error if it is unset.

## 2. Apply the Outfit

```sh
outfit apply examples/google-vertex/gemini-flash/Outfit
# or, from this directory:
outfit apply
```

The Outfit is:

```dockerfile
PROVIDER google-vertex
MODEL    gemini-2.0-flash
CONTEXT  1m
OUTPUT   8k
```

- `MODEL` is the Vertex Gemini model id opencode calls. Swap in another the
  project can serve, e.g. `gemini-1.5-pro`.
- `CONTEXT`/`OUTPUT` set opencode's context window and output cap. Gemini 2.0
  Flash takes a very large context (~1M tokens); adjust to taste.

## 3. Run it

```sh
opencode
```

Select `google-vertex/gemini-2.0-flash`. This provider is opencode-only; the Pi
harness does not support Vertex. For Claude on Vertex instead, see
[`../../google-vertex-anthropic/claude-sonnet`](../../google-vertex-anthropic/claude-sonnet).
