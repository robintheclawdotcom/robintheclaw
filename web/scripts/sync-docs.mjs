import { copyFileSync, existsSync, mkdirSync, readFileSync, rmSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const webRoot = join(dirname(fileURLToPath(import.meta.url)), "..");
const source = join(webRoot, "..", "docs");
const destination = join(webRoot, "public", "docs");
const published = [
  "control-plane-operations.md",
  "data-plane-archive.md",
  "execution-control-plane.md",
  "infrastructure-readiness.md",
  "mainnet-deployment.md",
  "production-audit-full-system.md",
  "research-methodology.md",
  "research-runtime.md",
  "venue-lighter.md",
];

if (!existsSync(source)) throw new Error("repository documentation directory is missing");
rmSync(destination, { recursive: true, force: true });
mkdirSync(destination, { recursive: true });
for (const file of published) {
  const input = join(source, file);
  if (!existsSync(input)) throw new Error(`published documentation is missing: ${file}`);
  if (/\btestnet\b/i.test(readFileSync(input, "utf8"))) {
    throw new Error(`published documentation contains legacy network copy: ${file}`);
  }
  copyFileSync(input, join(destination, file));
}
