# Contributing

Thanks for your interest in Hangar. You are welcome to fork and use the code under the
[AGPL-3.0 license](LICENSE.md), and issues are welcome as requests for features, bug
reports, or questions.

This is primarily a personal project for me in order to learn technologies they I am
unfamiliar with and exploring the limits of coding heavily with AI. Because of that,
external pull requests may not be reviewed or merged, but forking and adapting Hangar
for your own use is encouraged. The notes below remain as technical reference for the workflow.

## Development Setup

1. Fork the repository
2. Clone your fork: `git clone https://github.com/YOUR-USERNAME/Hangar.git`
3. Install dependencies: `go mod download`

## Code Standards

### Lint

You can run the following command to lint the code:

```bash
gofmt -w .
```

### Testing

Please include tests for new features or bug fixes.

## Commit & PR conventions

Commits and pull-request titles follow [Conventional Commits](https://www.conventionalcommits.org/)
— this is what drives automated versioning and the changelog (see **Releases**).

- Common types: `feat:` (new feature → minor bump), `fix:` (bug fix → patch bump),
  plus `docs:`, `refactor:`, `perf:`, `test:`, `build:`, `ci:`, `chore:`.
- Breaking changes: add `!` after the type (e.g. `feat!:`) or a `BREAKING CHANGE:`
  footer → major bump.
- PRs are **squash-merged**, so the **PR title** becomes the commit on `main`. A CI
  check (`PR Title`) enforces the conventional format on the title.

## Releases

Releases are **fully automated** — there is no manual version bump or tagging.

On every push to `main`, [`semantic-release`](https://github.com/semantic-release/semantic-release)
(`.github/workflows/release.yml`, config in `.releaserc.json`):

1. reads the Conventional Commits since the last release and computes the next
   semantic version;
2. updates `CHANGELOG.md` and bumps the version in `main.go` and
   `desktop/package.json` (via `scripts/set-version.mjs`), committing them as
   `chore(release): X.Y.Z [skip ci]`;
3. creates the `vX.Y.Z` tag and the GitHub release with generated notes.

Downstream jobs then build and attach the artifacts: **GoReleaser** produces the
signed Go binaries/checksums, and the desktop job builds the Windows NSIS
installer (`Hangar-Setup-X.Y.Z.exe`).

If no commit since the last release warrants a version change (only `docs`/`chore`/
`ci`/etc.), no release is cut — that is expected.

### Maintainer setup (one-time)
- Enable **squash merging** and set the squash commit message default to
  **"Pull request title"** (Settings → General → Pull Requests).
- If `main` is protected, create a `RELEASE_TOKEN` secret (a GitHub App token or PAT
  with `contents:write`) so the release commit/tag can be pushed; otherwise the
  workflow falls back to `GITHUB_TOKEN`.
- Ensure the baseline tag `v1.0.18` exists so the first automated release continues
  from the current version (rather than resetting to 1.0.0).

## Questions?

Feel free to open an issue for questions, bug reports, or feature requests.
