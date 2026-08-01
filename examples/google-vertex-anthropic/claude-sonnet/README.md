# Claude 3.5 Sonnet on GCP Vertex AI

Point opencode at Anthropic's Claude running on
[GCP Vertex AI](https://cloud.google.com/vertex-ai), using the [`Outfit`](Outfit)
in this directory. Vertex authenticates with your **Google credentials**
(Application Default Credentials), so there is no API key to set — `outfit`
injects none. It does need a **project**.

## Prerequisites

- A Google Cloud project with the **Vertex AI API** enabled, and the Claude
  model you want enabled in *Vertex AI → Model Garden* (Anthropic models are
  granted per-project).
- Application Default Credentials the SDK can find, via either:
  - `gcloud auth application-default login` (interactive), or
  - a service-account key file pointed to by `GOOGLE_APPLICATION_CREDENTIALS`.
- IAM permission to call the model (e.g. the *Vertex AI User* role, which
  includes `aiplatform.endpoints.predict`).

## 1. Set the project (and, optionally, the location)

The project is required — there is no default. The location defaults to
`global`; override it if you want a specific region:

```sh
export GOOGLE_VERTEX_PROJECT=my-gcp-project
export GOOGLE_VERTEX_LOCATION=us-east5     # optional; defaults to `global`
```

If `GOOGLE_VERTEX_PROJECT` is unset, `outfit apply` fails with a clear error
naming the option and the variable.

## 2. Apply the Outfit

```sh
outfit apply examples/google-vertex-anthropic/claude-sonnet/Outfit
# or, from this directory:
outfit apply
```

The Outfit is:

```dockerfile
PROVIDER google-vertex-anthropic
MODEL    claude-3-5-sonnet-v2@20241022
CONTEXT  200k
OUTPUT   8k
```

- `MODEL` is the Vertex model id opencode calls — note the `@date` form
  (publisher `anthropic`), unlike Bedrock's `anthropic.claude-…-v2:0`. Swap in
  another the project has enabled, e.g. `claude-3-5-haiku@20241022`.
- `CONTEXT`/`OUTPUT` set opencode's context window and output cap.

No key is written anywhere: the Vertex SDK resolves your credentials at run time.
The `project` and `location` are stored as provider options.

## 3. Run it

```sh
opencode
```

Select `google-vertex-anthropic/claude-3-5-sonnet-v2@20241022`. This provider is opencode-only;
the Pi harness does not support Vertex (it needs OAuth/ADC rather than an API
key). For Gemini on Vertex instead, see
[`../../google-vertex/gemini-flash`](../../google-vertex/gemini-flash).
