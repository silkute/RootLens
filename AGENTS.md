# Repository guidance

Read `rules.md` before making code changes. Reuse the existing protocol registry,
diagnostic engine, serialized storage writer and browser API helper. Update shared
usage guidance in place only when public caller behavior changes. Review its
applicable checklist after implementation without appending the results.

Keep fixtures synthetic and do not run stress tools against production devices.
UI text must not use uppercase styling or extra letter spacing.
