// Package inference holds the vocabulary for what an inference engine should
// serve, shared by every kind of node that runs one: a machine supervised by
// `spinloop daemon`, and a cloud environment driven through its control plane.
//
// It is deliberately a leaf — standard library only — so a node kind can be
// described without depending on how any other kind is reached. What is
// specific to reaching one kind stays with that kind: the AWS control plane in
// internal/cloud, the control API in internal/daemon.
package inference

// DeployConfig is what the deploy Lambda accepts: the runner-neutral
// description of WHAT to serve, derived from a Spinloop. Deliberately no
// weights prefix — the Lambda derives the S3 layout itself, and seeds the
// weights when they are not there yet, so this stays a statement of intent.
type DeployConfig struct {
	Runner      string `json:"runner"`
	ModelID     string `json:"modelId"`
	Quant       string `json:"quant"`
	ContextSize int    `json:"contextSize"`
	// Parallel is the number of concurrent request slots the engine should
	// run with, translated into the runner's own flag the same way a local
	// `spinloop serve` would — including scaling ContextSize for a llamacpp
	// runner, since llama.cpp divides its ctx-size budget across slots. Zero
	// means unset: no parallelism flag, ContextSize unscaled.
	Parallel        int      `json:"parallel,omitempty"`
	ServedModelName string   `json:"servedModelName"`
	ServeArgs       []string `json:"serveArgs"`
	// Companions names extra files from the model's own Hugging Face repo that
	// the engine loads beside the weights, keyed by role ("draft", "mmproj").
	// Values are bare filenames within that repo, never paths. Omitted when
	// empty, so a deployment naming none sends exactly what it always did.
	Companions map[string]string `json:"companions,omitempty"`
	// SpinloopVersion pins the spinloop release the instance's boot installs.
	// Empty means the boot installs the latest published release. Omitted
	// when empty, so an unpinned deploy sends exactly what it always did.
	SpinloopVersion string `json:"spinloopVersion,omitempty"`
	// InstanceType is the EC2 instance type the environment's instances launch
	// as. Empty means launch as the control plane's default type. It is a
	// property of the deployment, stored in the deploy config and read back on
	// the next fresh launch — a re-wake of a stopped instance keeps the type it
	// was launched with. Omitted when empty, so an untyped deploy sends exactly
	// what it always did.
	InstanceType string `json:"instanceType,omitempty"`
}
