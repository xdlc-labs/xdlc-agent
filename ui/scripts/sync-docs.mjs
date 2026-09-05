#!/usr/bin/env node
/** Sync repo docs/ → ui/content/docs + ui/public/docs-assets (Vite-served images). */
import { copyFileSync, cpSync, mkdirSync, readdirSync, rmSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const uiRoot = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const srcDir = path.resolve(uiRoot, "../docs");
const destDir = path.resolve(uiRoot, "content/docs");
const assetsSrc = path.join(srcDir, "assets");
const assetsDest = path.resolve(uiRoot, "public/docs-assets");

const skip = new Set(["CHANGELOG.md", "CODE_OF_CONDUCT.md", "README.md"]);

mkdirSync(destDir, { recursive: true });
for (const name of readdirSync(srcDir)) {
  if (!name.endsWith(".md") || skip.has(name)) continue;
  copyFileSync(path.join(srcDir, name), path.join(destDir, name));
}

rmSync(assetsDest, { recursive: true, force: true });
cpSync(assetsSrc, assetsDest, { recursive: true });

console.log(
  `sync-docs: ${readdirSync(destDir).length} md → content/docs; assets → public/docs-assets`,
);
