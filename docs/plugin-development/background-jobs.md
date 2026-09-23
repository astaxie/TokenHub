# Background Job Plugins

Language: English | [简体中文](../zh-CN/plugin-development/background-jobs.md) | [日本語](../ja/plugin-development/background-jobs.md)

The background-job contract covers work that does not belong on the model request path, such as quota refresh, account synchronization, cleanup, reporting, and health checks. A registered in-process job may run on a declared schedule or be triggered by an operator from Plugin Management; external command jobs cannot currently be registered.

> **Runtime availability:** This page defines the external background-job contract. Packages and schedules can be validated, but the current TokenHub runtime rejects external background commands before launch because host-level isolation is not yet enforceable. In-process built-in jobs are unaffected.

## Declare a Job

Declare `extension` as the kind, `background` as the placement, and add one or more `capabilities.background_jobs` entries:

```yaml
kinds: [extension]
placement: [background]
entry:
  backend:
    protocol: stdio-json-v1
    command: bin/your-plugin
capabilities:
  background_jobs:
    - id: quota.refresh
      title: Refresh quota
      capability: provider.quota.refresh
      subject: example-provider
      schedule: "@startup"
      timeout_millis: 5000
      max_concurrency: 1
      retry:
        max_attempts: 2
        backoff_millis: 1000
      input_schema:
        type: object
        required: [resource_id]
        properties:
          resource_id:
            type: string
      output_schema:
        type: object
```

Treat the plugin ID and job ID as stable compatibility contracts. Keep inputs small, use explicit JSON schemas, bound the timeout and concurrency, and choose retry behavior that is safe when work has partially completed.

## Implement the Handler

`ServeBackgroundJob` reads one `stdio-json-v1` invocation from standard input and writes one structured result to standard output. The invocation includes the plugin ID, job ID, trigger, actor, and payload. Send logs only to standard error, return sanitized data, and never include credentials or raw provider responses in results.

Prefer idempotent handlers. If a job makes an external change, use a stable operation key or persist enough state to make a retry safe. A schedule is not a guarantee of exactly-once execution.

## Test It

The Devkit includes a complete fixture and contract command:

```bash
cd plugin-devkit
go test ./...
go run ./cmd/tokenhub-plugin-test background \
  --package "$PWD/examples/background-heartbeat-go"
```

Copy the fixture into a separate plugin workspace, then update the manifest, handler checks, schemas, and tests together. Use the Devkit output to verify the declared job contract. After installation in the current TokenHub release, the declaration remains visible under collapsed **Developer Information** on the plugin's **Details** page, but the package is marked **Startup Failed** and the scheduler does not register or run the job. Declared schedules and operator-triggered controls describe the future external runtime contract only.

See the [complete guide](guide.md) for the invocation example and the [Packaging and Release](packaging-and-release.md) guide before distribution.
