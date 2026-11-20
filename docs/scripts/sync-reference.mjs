// Copies the plain protocol references and the packaging record from docs/*.md
// into reference/ (git-ignored) for the 'reference' docs instance. The files
// stay canonical at docs/<name>.md because Go code, apis/asyncapi.yaml, SPEC.md
// and IQVO/asrs-control cite them at those paths; a docs instance cannot use
// docs/ itself as its content path (its MDX loader would also claim every page
// of the main instance under docs/docs/).
import {copyFileSync, mkdirSync, rmSync} from 'node:fs';
import {dirname, join} from 'node:path';
import {fileURLToPath} from 'node:url';

const site = join(dirname(fileURLToPath(import.meta.url)), '..');
const pages = ['vda5050-subset.md', 'telegram-protocol.md', 'before-first-release.md'];
const out = join(site, 'reference');
rmSync(out, {recursive: true, force: true});
mkdirSync(out, {recursive: true});
for (const p of pages) {
  copyFileSync(join(site, p), join(out, p));
}
console.log(`sync-reference: ${pages.length} pages -> reference/`);
