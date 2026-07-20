import { copyFile, mkdir, readdir, rm, stat } from "node:fs/promises";
import { dirname, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const tablerJS = resolve(repoRoot, "node_modules/@tabler/core/dist/js/tabler.min.js");
const iconFonts = resolve(repoRoot, "node_modules/@tabler/icons-webfont/dist/fonts");
const requiredIconFont = resolve(iconFonts, "tabler-icons.woff2");
const staticDir = resolve(repoRoot, "internal/web/static");
const targetFonts = resolve(staticDir, "fonts");

async function requirePath(path, kind) {
  let metadata;
  try {
    metadata = await stat(path);
  } catch (error) {
    if (error.code === "ENOENT") {
      throw new Error(`missing Tabler ${kind}: ${path}`);
    }
    throw new Error(`cannot read Tabler ${kind} at ${path}: ${error.message}`);
  }
  return metadata;
}

async function requireFile(path, kind) {
  const metadata = await requirePath(path, kind);
  if (!metadata.isFile()) {
    throw new Error(`expected Tabler ${kind} to be a file: ${path}`);
  }
}

async function listFiles(root, directory = root) {
  const entries = await readdir(directory, { withFileTypes: true });
  entries.sort((left, right) => left.name < right.name ? -1 : left.name > right.name ? 1 : 0);

  const files = [];
  for (const entry of entries) {
    const path = resolve(directory, entry.name);
    if (entry.isDirectory()) {
      files.push(...(await listFiles(root, path)));
      continue;
    }
    if (!entry.isFile()) {
      throw new Error(`unsupported entry in Tabler icon fonts: ${path}`);
    }
    files.push(relative(root, path));
  }
  return files;
}

async function syncTablerAssets() {
  await requireFile(tablerJS, "JavaScript source");
  const fontsMetadata = await requirePath(iconFonts, "icon fonts directory");
  if (!fontsMetadata.isDirectory()) {
    throw new Error(`expected Tabler icon fonts directory: ${iconFonts}`);
  }
  await requireFile(requiredIconFont, "icon font source");

  const fontFiles = await listFiles(iconFonts);
  if (fontFiles.length === 0) {
    throw new Error(`no Tabler icon fonts found in: ${iconFonts}`);
  }

  await mkdir(staticDir, { recursive: true });
  await copyFile(tablerJS, resolve(staticDir, "tabler.min.js"));
  await rm(targetFonts, { recursive: true, force: true });

  for (const fontFile of fontFiles) {
    const source = resolve(iconFonts, fontFile);
    const target = resolve(targetFonts, fontFile);
    await mkdir(dirname(target), { recursive: true });
    await copyFile(source, target);
  }
}

syncTablerAssets().catch((error) => {
  console.error(`sync-tabler-assets: ${error.message}`);
  process.exitCode = 1;
});
