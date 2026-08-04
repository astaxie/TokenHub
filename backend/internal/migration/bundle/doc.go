// Package bundle defines the CanonicalMigrationBundle: the versioned,
// source-agnostic intermediate representation that travels between a
// source adapter (LiteLLM, one-api, ...) and the TokenHub sink.
//
// The bundle is a plain JSON document and never contains plaintext
// secrets. Every secret field must be represented as a SecretRef of
// the form {"$secretRef": "ENV_NAME"} and resolved at apply time via
// a SecretResolver.
//
// The specification lives in docs/migration/bundle-schema.md and is
// enforced by bundle.schema.json (embedded, validated with the
// jsonschema/v6 library).
package bundle
