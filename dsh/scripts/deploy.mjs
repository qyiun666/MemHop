#!/usr/bin/env node
// dsh/scripts/deploy.mjs
//
// 部署 MemHop DSH 插件到 DSH profiles：
//   1. 将 dsh/plugins/<pkg>/ 复制到 <profiles>/node_modules/@deepseek-ai/<pkg>/
//   2. （可选）检查 cordis.patch.yml 是否已注册插件
//
// 用法：
//   node dsh/scripts/deploy.mjs [--profiles <dir>]
//
// 默认 profiles 目录：~/Library/Application Support/dsh-desktop/harness/profiles
// 可通过环境变量 DSH_PROFILES_DIR 覆盖。
//
// 注意：修改 cordis.patch.yml 后需要重启 DSH Desktop 才会加载新插件。

import { cp, readdir, readFile, access, mkdir } from "node:fs/promises";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import os from "node:os";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const pluginsDir = join(root, "plugins");
const defaultProfiles = join(
  os.homedir(),
  "Library",
  "Application Support",
  "dsh-desktop",
  "harness",
  "profiles"
);
const profilesDir = process.env.DSH_PROFILES_DIR || defaultProfiles;

const args = process.argv.slice(2);
const idx = args.indexOf("--profiles");
if (idx !== -1 && args[idx + 1]) {
  process.env.DSH_PROFILES_DIR = args[idx + 1];
}

const nmDir = join(profilesDir, "node_modules", "@deepseek-ai");
const patchFile = join(profilesDir, "web", "cordis.patch.yml");

async function main() {
  console.log(`[deploy] profiles: ${profilesDir}`);
  try {
    await access(profilesDir);
  } catch {
    console.error(`[deploy] profiles dir not found: ${profilesDir}`);
    process.exit(1);
  }

  const entries = await readdir(pluginsDir, { withFileTypes: true });
  const pkgs = entries.filter((e) => e.isDirectory());
  if (pkgs.length === 0) {
    console.log("[deploy] no plugins under dsh/plugins/");
    return;
  }

  await mkdir(nmDir, { recursive: true });

  for (const pkg of pkgs) {
    const src = join(pluginsDir, pkg.name);
    const dest = join(nmDir, pkg.name);
    // 读取 package.json 确认真实包名（目录名可能不同）。
    let pkgName = pkg.name;
    try {
      const manifest = JSON.parse(await readFile(join(src, "package.json"), "utf8"));
      pkgName = manifest.name;
    } catch {
      /* 无 package.json 的目录跳过 */
      console.log(`[deploy] skip ${pkg.name} (no package.json)`);
      continue;
    }
    if (!pkgName.startsWith("@deepseek-ai/")) {
      console.log(`[deploy] skip ${pkg.name}: package name ${pkgName} is not @deepseek-ai/*`);
      continue;
    }
    const finalDest = join(nmDir, pkgName.split("/")[1]);
    await cp(src, finalDest, { recursive: true, force: true });
    console.log(`[deploy] installed ${pkgName} -> ${finalDest}`);
  }

  // 检查 cordis.patch.yml 是否注册了插件（只读提示，不做自动修改）。
  try {
    const patch = await readFile(patchFile, "utf8");
    for (const pkg of pkgs) {
      let pkgName = pkg.name;
      try {
        const manifest = JSON.parse(await readFile(join(pluginsDir, pkg.name, "package.json"), "utf8"));
        pkgName = manifest.name;
      } catch {
        continue;
      }
      if (!patch.includes(pkgName)) {
        console.warn(`[deploy] WARN: ${pkgName} not registered in ${patchFile}`);
        console.warn(`         add an insert entry (name: ${pkgName}) then restart DSH Desktop`);
      } else {
        console.log(`[deploy] ${pkgName} already registered in cordis.patch.yml`);
      }
    }
  } catch {
    console.warn(`[deploy] WARN: cannot read ${patchFile}`);
  }

  console.log("[deploy] done. Restart DSH Desktop to load plugin changes.");
}

main().catch((err) => {
  console.error("[deploy] failed:", err);
  process.exit(1);
});
