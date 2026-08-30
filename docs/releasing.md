# Releasing the toolkit

## How a release happens

1. The repository owner pushes a tag `vX.Y.Z` on `main`.
2. The tag push runs `.github/workflows/release.yml`. goreleaser builds
   `tfpfgen` for linux, darwin and windows on amd64 and arm64 — pure Go, no
   cgo, `-trimpath` — stamps `internal/version` with the release version, and
   publishes the archives plus a SHA-256 checksums file as the GitHub release
   for that tag. Nothing is signed; consumers verify against the checksums
   file.
3. If the tag is stable semver (no prerelease suffix), a second job
   fast-forwards the moving major tag: `v1.2.3` force-moves `v1` to the same
   commit, `v0.4.1` moves `v0`. Prerelease tags such as `v1.3.0-rc.1` publish
   binaries but never move a major tag.

A breaking change bumps the major, which starts a new moving tag (`v2`) and
leaves every `v1` caller untouched.

## What consumers pin

- **Provider repos** pin an exact release tag in config
  (`generator.version: v1.2.3`). The `setup-tfpfgen` action downloads that
  release's binary for the runner, verifies it against the release's
  checksums file, and does not accept a binary that does not report the requested
  version. Branches are never accepted.
- **Caller workflows** reference this repo's reusable workflows and actions
  at the moving major tag, e.g. `@v1`. Compatible releases reach them when
  `v1` fast-forwards; a breaking change never does, because it ships as
  `v2`.

Note the stamped version has no leading `v`: `tfpfgen version` for tag
`v0.1.0` prints `0.1.0`. A source build prints `dev`, which a pinned
pipeline treats as a failure.
