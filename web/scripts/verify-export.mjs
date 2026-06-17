#!/usr/bin/env node
// Post-build verification for the static export (web/out).
// Validates Hangar branding, basePath-correct assets/links, in-page anchors,
// social-card metadata, and that no stale upstream branding leaked in.
// Run after `next build` (output: "export"). Exits non-zero on any failure.

import { readFileSync, existsSync, readdirSync } from "node:fs";
import { join, dirname, extname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const OUT = resolve(__dirname, "..", "out");
const BASE = "/Hangar"; // production basePath (repo name)

const errors = [];
const fail = (m) => errors.push(m);

if (!existsSync(OUT)) {
  console.error(`verify-export: ${OUT} not found — run "npm run build" first.`);
  process.exit(1);
}

function walk(dir) {
  const out = [];
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, e.name);
    if (e.isDirectory()) out.push(...walk(p));
    else out.push(p);
  }
  return out;
}

const allFiles = walk(OUT);
const htmlFiles = allFiles.filter((f) => f.toLowerCase().endsWith(".html"));
const indexPath = join(OUT, "index.html");
if (!existsSync(indexPath)) fail("out/index.html is missing");
const index = existsSync(indexPath) ? readFileSync(indexPath, "utf8") : "";

// --- 1) Branding present on the home page ---------------------------------
if (!/<title>[^<]*Hangar[^<]*<\/title>/i.test(index))
  fail('index.html <title> does not contain "Hangar"');

const h1s = [...index.matchAll(/<h1[^>]*>([\s\S]*?)<\/h1>/gi)].map((m) => m[1]);
if (h1s.length === 0) fail("index.html has no <h1>");
else if (!h1s.some((h) => /Hangar/i.test(h) || /alt="[^"]*Hangar/i.test(h)))
  fail('no <h1> on index.html references "Hangar" (text or logo alt)');

// --- 2) No stale branding leaks across all HTML ---------------------------
for (const f of htmlFiles) {
  const html = readFileSync(f, "utf8");
  const rel = f.slice(OUT.length + 1);
  if (/claude\s+squad/i.test(html))
    fail(`${rel}: contains the old product name "claude squad"`);
  if (/hanger/i.test(html))
    fail(`${rel}: contains the misspelling "hanger" (should be "Hangar")`);
  // "smtg-ai" is only allowed as part of the upstream repo ref "smtg-ai/claude-squad"
  // (the Unix install commands + the footer attribution link/text).
  const scrub = html.replace(/smtg-ai\/claude-squad/g, "");
  if (/smtg-ai/.test(scrub))
    fail(`${rel}: "smtg-ai" appears outside the upstream "smtg-ai/claude-squad" reference`);
}

// --- 3) Social-card metadata ---------------------------------------------
const meta = (name) => {
  const m =
    index.match(new RegExp(`<meta[^>]+(?:property|name)="${name}"[^>]*content="([^"]+)"`, "i")) ||
    index.match(new RegExp(`<meta[^>]+content="([^"]+)"[^>]*(?:property|name)="${name}"`, "i"));
  return m ? m[1] : null;
};
const ogImage = meta("og:image");
const twImage = meta("twitter:image");
const twCard = meta("twitter:card");
if (!ogImage) fail('missing <meta property="og:image">');
if (!twImage) fail('missing <meta name="twitter:image">');
if (twCard !== "summary_large_image")
  fail(`twitter:card should be "summary_large_image" (got "${twCard}")`);
for (const [label, url] of [["og:image", ogImage], ["twitter:image", twImage]]) {
  if (!url) continue;
  const idx = url.indexOf(BASE + "/");
  if (idx === -1) fail(`${label} URL "${url}" does not include basePath "${BASE}/"`);
  else {
    const rel = url.slice(idx + BASE.length).split(/[?#]/)[0];
    if (!existsSync(join(OUT, rel))) fail(`${label} asset not found on disk: out${rel}`);
  }
}

// --- 4) basePath-correct asset/link existence (all HTML) ------------------
function resolveRef(relWithBase) {
  let rel = relWithBase.slice(BASE.length).split(/[?#]/)[0]; // strip basePath, query, hash
  if (rel === "" || rel === "/") return join(OUT, "index.html");
  if (rel.endsWith("/")) return join(OUT, rel, "index.html");
  if (extname(rel) !== "") return join(OUT, rel);
  // extensionless route
  const asHtml = join(OUT, rel + ".html");
  return existsSync(asHtml) ? asHtml : join(OUT, rel, "index.html");
}
// Only validate LOCAL (root-relative) refs that start with the basePath — i.e. real
// href/src/srcset values, not "/Hangar" substrings inside external GitHub URLs.
const localRefs = new Set();
const attrRe = /(?:href|src)="([^"]*)"/g;
const srcsetRe = /(?:srcset|imagesrcset)="([^"]*)"/gi;
for (const f of htmlFiles) {
  const html = readFileSync(f, "utf8");
  for (const m of html.matchAll(attrRe)) {
    if (m[1].startsWith(BASE)) localRefs.add(m[1]);
  }
  for (const m of html.matchAll(srcsetRe)) {
    for (const part of m[1].split(",")) {
      const url = part.trim().split(/\s+/)[0];
      if (url && url.startsWith(BASE)) localRefs.add(url);
    }
  }
}
let checkedRefs = 0;
for (const ref of localRefs) {
  checkedRefs++;
  if (!existsSync(resolveRef(ref))) fail(`basePath reference does not resolve to a file: ${ref}`);
}

// --- 5) In-page anchors resolve ------------------------------------------
for (const required of ["features", "install"]) {
  if (!new RegExp(`id="${required}"`).test(index))
    fail(`index.html is missing id="${required}" (in-page nav target)`);
}
for (const f of htmlFiles) {
  const html = readFileSync(f, "utf8");
  const rel = f.slice(OUT.length + 1);
  const ids = new Set([...html.matchAll(/id="([^"]+)"/g)].map((m) => m[1]));
  for (const m of html.matchAll(/href="#([^"]+)"/g)) {
    const frag = m[1];
    if (frag && !ids.has(frag)) fail(`${rel}: in-page link #${frag} has no matching id`);
  }
}

// --- report ---------------------------------------------------------------
if (errors.length) {
  console.error(`\nverify-export: FAILED with ${errors.length} issue(s):`);
  for (const e of errors) console.error("  ✗ " + e);
  process.exit(1);
}
console.log(
  `verify-export: OK — ${htmlFiles.length} HTML file(s), ${checkedRefs} basePath ref(s) resolved, ` +
    `branding + social card + anchors verified.`
);
