# Changelog

All notable changes per release. Versions follow [semver](https://semver.org).

## v1.1.2 — 2026-07-27

- Added self-hosted version and license badges; wired a badges job into pipeline.yml.

## v1.1.1 — 2026-07-27

Add a LICENSE file.

- Add `LICENSE` (MIT). The project previously shipped without a license file; the
  tool's source is now explicitly MIT-licensed. Vendored dependencies under
  `vendor/` retain their own upstream licenses.

## v1.1.0 — 2026-03-29

- Add a `-file` flag for a custom output file name.

## v1.0.0 — 2026-01-28

Initial release. Extracts typed constants from an OpenAPI spec's `x-constants`
extension and generates a Go file with properly typed constants.
