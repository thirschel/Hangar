#!/usr/bin/env node
// Propagates a release version to every place the repo hard-codes it, so the Go
// core-daemon and the Electron desktop app always report the same semver.
// Invoked by semantic-release (@semantic-release/exec prepareCmd) with the next
// version, before the release is committed and tagged.
import { readFileSync, writeFileSync } from 'node:fs';

const version = process.argv[2];
if (!/^\d+\.\d+\.\d+/.test(version ?? '')) {
  console.error(`set-version: expected a semver argument, got "${version ?? ''}"`);
  process.exit(1);
}

// 1) Go daemon: main.go `version = "x.y.z"`.
const mainGo = 'main.go';
const src = readFileSync(mainGo, 'utf8');
const re = /(version\s*=\s*")\d+\.\d+\.\d+(")/;
if (!re.test(src)) {
  console.error(`set-version: version literal not found in ${mainGo}`);
  process.exit(1);
}
writeFileSync(mainGo, src.replace(re, `$1${version}$2`));

// 2) Desktop app: package.json + its lockfile. Edited in place (npm's 2-space +
// trailing-newline format is reproduced exactly, so only the version lines change).
function setJsonVersion(file, mutate) {
  const json = JSON.parse(readFileSync(file, 'utf8'));
  mutate(json);
  writeFileSync(file, JSON.stringify(json, null, 2) + '\n');
}
setJsonVersion('desktop/package.json', (j) => {
  j.version = version;
});
setJsonVersion('desktop/package-lock.json', (j) => {
  j.version = version;
  if (j.packages && j.packages['']) j.packages[''].version = version;
});

console.log(`set-version: set version ${version} in main.go and desktop/`);
