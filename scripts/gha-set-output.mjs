#!/usr/bin/env node
// Exposes the freshly published release version to later GitHub Actions jobs via
// $GITHUB_OUTPUT (read as outputs of the step that runs semantic-release). No-op
// outside CI (GITHUB_OUTPUT unset), e.g. a local `--dry-run`. Invoked by
// semantic-release (@semantic-release/exec successCmd) only when a release is cut.
import { appendFileSync } from 'node:fs';

const out = process.env.GITHUB_OUTPUT;
const version = process.argv[2];
if (out && version) {
  appendFileSync(out, `published=true\nversion=${version}\ntag=v${version}\n`);
}
