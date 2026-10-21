# Third-party notices

RootLens's original code is MIT licensed. Dependency licenses remain applicable.

- Vue, Vite, Tailwind CSS, Reka UI and uPlot: MIT licenses in their distributions.
- Go standard library and golang.org/x packages: BSD-style licenses.
- modernc.org/sqlite and transitive packages: see their respective module licenses;
  SQLite itself is public domain.
- Inter: SIL Open Font License 1.1. Bundled locally via `@fontsource/inter`.
  The font license is included in `docs/licenses/Inter-OFL.txt`.
- HAProxy is a separate optional deployment dependency under its upstream license.
  No HAProxy code is incorporated into RootLens binaries.

Beszel is credited as the principal design reference. RootLens does not incorporate
Beszel source, logos, screenshots, fonts, or other assets. The screenshots generated
for this repository depict RootLens itself.

Release SBOMs identify resolved dependency versions. Preserve dependency license
notices when redistributing source, fonts, binaries or images.

Resolved dependency license texts, including the Go standard library and frontend
packages, are bundled under `docs/licenses/`. Images include these notices under
`/usr/share/licenses/rootlens/`. Some notices describe build/test dependencies that
are not present in the production JavaScript bundle.
