# Contributing

Read `rules.md` before changing shared contracts. Keep PRs focused on a measurable
behavior, and include a failing regression case when fixing telemetry or diagnosis.

Use Go 1.27.3+, Node 22.12+, npm and a disposable Linux test environment. Run the
commands in the README. `go run ./cmd/contracts` regenerates the public schemas and
browser types. Do not edit generated contract files by hand. Build the frontend
before building the hub; embedded assets are included in the repository for a
usable Go checkout and must match their source.

Collector fixtures must not contain hostnames, command lines, credentials, container
environment variables, arbitrary labels or private file paths. Explain counter
units, denominator, kernel capability and reset behavior in telemetry changes.

Do not add a rule that infers a cause from utilization alone. Supply positive,
negative, missing-data, recovery and reset examples. Changed rule meaning requires
a version increment and preservation of old reports.

UI text uses sentence case, normal spacing and no uppercase styling. Validate
keyboard behavior, both themes and widths of 360, 390, 768 and 1440 pixels. Charts
need a textual alternative and must not connect missing intervals.

Format Go with gofmt. Preserve unrelated work. Describe fixture, build, Linux, device
and production validation separately; a cross-compile is not a native hardware test.

By contributing you agree to license your contribution under the repository's MIT
license and confirm that you have the right to submit it. No CLA is required.
