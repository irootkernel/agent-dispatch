# JSON Schemas

These Draft 2020-12 schemas are executable contracts owned by the specifications
role and refined by [contracts](../contracts/README.md). Keep their established
filenames, `$id` values, cross-schema references, and versioned record semantics
in sync with the Go producers and consumers. Schema changes are product-contract
changes, not documentation reformatting.

Start with [configuration](config.schema.json),
[dispatch intent](dispatch-intent.schema.json), and
[work receipt](work-receipt.schema.json), then the relevant record family.
[Examples](../examples/README.md) supply representative instances.

Run `make schema-validation` from the repository root. It compiles schemas with
format assertions and validates the covered examples and capability report.
Runtime semantic checks and migration compatibility require their owning Go tests.
