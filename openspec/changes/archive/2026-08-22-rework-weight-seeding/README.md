# rework-weight-seeding

Rework model weight seeding into a first-class, observable, self-terminating job: a streaming Node.js seeder on a no-bake AL2023 instance, EMF-based status in CloudWatch, an idempotent seed Lambda with start/status/list/stop, and a _seed.json manifest replacing the per-runner sentinel.
