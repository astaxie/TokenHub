# Raccoon

Connect an explicitly authorized Raccoon account to TokenHub.

- Plugin ID: `tokenhub.provider.raccoon-subscription`
- Configuration: Provider management, with a separate account resource
- Lifecycle: installed, enabled, configured, and used independently
- Authentication: available browser/device flows or explicit session JSON import
- Credentials: encrypted per-account session; refresh rotations persist before acknowledgement
- Runtime: Node.js 22.23.1 or later; isolated account process with host-controlled network access
- Models: account model discovery; public access through OpenAI-compatible chat completions

The bundled upstream adapter's license and provenance are retained in
`backend/internal/subscriptionworker/vendor/`.
