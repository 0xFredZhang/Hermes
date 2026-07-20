import { copyFile, mkdir, rm, stat } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const tablerJS = resolve(repoRoot, "node_modules/@tabler/core/dist/js/tabler.min.js");
const iconFonts = resolve(repoRoot, "node_modules/@tabler/icons-webfont/dist/fonts");
const iconFontFiles = ["tabler-icons.ttf", "tabler-icons.woff", "tabler-icons.woff2"];
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

async function syncTablerAssets() {
  await requireFile(tablerJS, "JavaScript source");
  const fontsMetadata = await requirePath(iconFonts, "icon fonts directory");
  if (!fontsMetadata.isDirectory()) {
    throw new Error(`expected Tabler icon fonts directory: ${iconFonts}`);
  }
  for (const iconFont of iconFontFiles) {
    await requireFile(resolve(iconFonts, iconFont), "icon font source");
  }

  await mkdir(staticDir, { recursive: true });
  await copyFile(tablerJS, resolve(staticDir, "tabler.min.js"));
  await rm(targetFonts, { recursive: true, force: true });

  await mkdir(targetFonts, { recursive: true });
  for (const iconFont of iconFontFiles) {
    await copyFile(resolve(iconFonts, iconFont), resolve(targetFonts, iconFont));
  }
}

syncTablerAssets().catch((error) => {
  console.error(`sync-tabler-assets: ${error.message}`);
  process.exitCode = 1;
});
