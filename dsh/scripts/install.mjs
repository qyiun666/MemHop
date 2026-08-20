#!/usr/bin/env node
// dsh/scripts/install.mjs — 一键部署 MemHop DSH 插件(单插件 dsh-memhop)。
//
// 做的事:
//   1. 把 dsh/plugins/<pkg>/ 部署到 <profiles>/node_modules/@deepseek-ai/<pkg>/
//   2. 更新 cordis.patch.yml:把旧的 memhop-core / memhop-ui 两条注册替换为
//      单条 dsh-memhop 注册(带默认 config);修改前自动备份 patch 文件
//   3. 移除已废弃的旧部署包(dsh-memhop-core / dsh-client-memhop-ui)
//   4. (可选 --launchd)安装 memhop-mcp 常驻服务:wrapper + launchd plist + bootstrap
//
// 用法:
//   node dsh/scripts/install.mjs [--profiles <dir>] [--launchd] [--no-patch]
//
// 默认 profiles 目录:~/Library/Application Support/dsh-desktop/harness/profiles
// 可通过环境变量 DSH_PROFILES_DIR 覆盖。
//
// 注意:改完 cordis.patch.yml 后需重启 DSH Desktop 生效。

import { cp, readdir, readFile, writeFile, access, mkdir, rm } from "node:fs/promises";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
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
if (idx !== -1 && args[idx + 1]) process.env.DSH_PROFILES_DIR = args[idx + 1];
const withLaunchd = args.includes("--launchd");
const skipPatch = args.includes("--no-patch");

const nmDir = join(profilesDir, "node_modules", "@deepseek-ai");
const patchFile = join(profilesDir, "web", "cordis.patch.yml");

/** 已废弃的旧插件包(合并前)与对应的 patch 条目 id。 */
const LEGACY_PKGS = ["dsh-memhop-core", "dsh-client-memhop-ui"];
const LEGACY_IDS = ["memhop-core", "memhop-ui"];

const NEW_INSERT = `# ---- MemHop 记忆子系统(单插件 dsh-memhop:控制面+UI+服务器管理)----
# 一个 DSH 会话 = 一个 Agent = 一个独立 .meh(dbDir/<session-id>.meh)。
# 插件职责:31 个 mcp__memhop__* 工具注册到 agent 作用域;每轮自动
# search/update、按策略 dream;记忆快照注入 system prompt(P2)与
# 历史窗口控制(P3);memhop-mcp 服务器/launchd 管理;Web「记忆」面板。
- insert:
    - id: memhop
      name: '@deepseek-ai/dsh-memhop'
      config:
        serverUrl: http://127.0.0.1:3939
        dbDir: ~/.memhop/agents
        toolCallTimeoutMs: 120000
        autoSearch: true
        autoUpdate: true
        dreamEveryTurns: 20
        idleDreamMs: 600000
        snapshotMaxChars: 16000
        promptSnapshot: true
        windowControl: true
`;

const PLIST_PATH = join(os.homedir(), "Library", "LaunchAgents", "com.memhop.mcp.plist");
const WRAPPER_PATH = join(os.homedir(), ".memhop", "memhop-mcp.sh");
const OUT_LOG = join(os.homedir(), ".memhop", "server.out.log");
const ERR_LOG = join(os.homedir(), ".memhop", "server.err.log");
const ENV_FILE = join(os.homedir(), ".memhop", "server.env");
const DB_DIR = join(os.homedir(), ".memhop", "agents");

function homeDir() {
  return os.homedir();
}

function detectBin() {
  // 显式配置 > wrapper exec 行 > PATH
  if (process.env.MEMHOP_MCP_BIN) return process.env.MEMHOP_MCP_BIN;
  try {
    for (const line of readFileSync(WRAPPER_PATH, "utf8").split("\n")) {
      const m = line.match(/exec\s+(['"]?)([^'"]+)\1/);
      if (m) return m[2].trim();
    }
  } catch {
    /* noop */
  }
  return "memhop-mcp";
}

function plistTemplate() {
  return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.memhop.mcp</string>
  <key>ProgramArguments</key>
  <array><string>${WRAPPER_PATH}</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>${OUT_LOG}</string>
  <key>StandardErrorPath</key><string>${ERR_LOG}</string>
</dict>
</plist>
`;
}

function wrapperTemplate(bin) {
  const model = process.env.MEMHOP_EMBED_MODEL || "qllama/bge-m3:q4_k_m";
  const enc = process.env.MEMHOP_ENCODER_ADDR || "http://127.0.0.1:11434";
  return `#!/bin/bash
set -a; source "${ENV_FILE}"; set +a
exec "${bin}" -db-dir "${DB_DIR}" -embed-model "${model}" -encoder-addr "${enc}" -transport streamable-http -listen "127.0.0.1:3939"
`;
}

/** 用新段落替换 patch 中的旧 MemHop 段落;找不到则追加到文件末尾。幂等:
 *  已是最新(含 dsh-memhop 且无旧标记)时跳过。 */
function patchCordisYml() {
  const old = readFileSync(patchFile, "utf8");
  if (!old.includes("memhop-core") && !old.includes("memhop-ui")) {
    if (old.includes("@deepseek-ai/dsh-memhop")) {
      console.log("[install] cordis.patch.yml: already single-plugin (dsh-memhop), no change");
      return;
    }
    // 全新安装:直接追加
    const out = old.trimEnd() + "\n\n" + NEW_INSERT;
    writeFileSync(patchFile, out);
    console.log("[install] cordis.patch.yml: appended dsh-memhop insert");
    return;
  }
  // 定位旧段落起始:MemHop 相关注释或旧条目。
  let startIdx = -1;
  const lines = old.split("\n");
  for (let i = 0; i < lines.length; i += 1) {
    if (lines[i].includes("MemHop 记忆子系统") || lines[i].includes("memhop-core") || lines[i].includes("memhop-ui")) {
      startIdx = i;
      break;
    }
  }
  if (startIdx === -1) {
    console.warn("[install] WARN: legacy markers found but no start line; patch left untouched");
    return;
  }
  // 段落结束:起始行之后第一个顶层条目(形如 "- id: xxx",不带缩进;
  // MemHop 段落内的顶层条目只有 "- insert:",insert 子项带 4 空格缩进)。
  let endIdx = lines.length;
  for (let i = startIdx + 1; i < lines.length; i += 1) {
    if (/^-\s+id:/.test(lines[i])) {
      endIdx = i;
      break;
    }
  }
  const head = lines.slice(0, startIdx).join("\n");
  const tail = lines.slice(endIdx).join("\n");
  const out = head.trimEnd() + "\n\n" + NEW_INSERT + (tail.trim() ? tail.trimEnd() + "\n" : "");
  writeFileSync(patchFile, out);
  console.log(`[install] cordis.patch.yml: replaced legacy MemHop section (lines ${startIdx + 1}..${endIdx}) with single dsh-memhop insert`);
}

function installLaunchd() {
  const bin = detectBin();
  console.log(`[install] launchd: serverBin=${bin}`);
  writeFileSync(WRAPPER_PATH, wrapperTemplate(bin));
  spawnSync("chmod", ["755", WRAPPER_PATH]);
  writeFileSync(PLIST_PATH, plistTemplate());
  const uid = process.getuid ? process.getuid() : 501;
  const r = spawnSync("launchctl", ["bootstrap", `gui/${uid}`, PLIST_PATH], { encoding: "utf8" });
  const loaded =
    r.status === 0 ||
    spawnSync("launchctl", ["print", `gui/${uid}/com.memhop.mcp`], { encoding: "utf8" }).status === 0;
  console.log(loaded ? "[install] launchd: installed & loaded com.memhop.mcp" : `[install] launchd: installed but not loaded (${String(r.stderr || r.stdout || "?")})`);
  return loaded;
}

async function main() {
  console.log(`[install] profiles: ${profilesDir}`);
  try {
    await access(profilesDir);
  } catch {
    console.error(`[install] profiles dir not found: ${profilesDir}`);
    process.exit(1);
  }

  await mkdir(nmDir, { recursive: true });
  const entries = await readdir(pluginsDir, { withFileTypes: true });
  const pkgs = entries.filter((e) => e.isDirectory());
  for (const pkg of pkgs) {
    const src = join(pluginsDir, pkg.name);
    let pkgName = pkg.name;
    try {
      pkgName = JSON.parse(await readFile(join(src, "package.json"), "utf8")).name;
    } catch {
      console.log(`[install] skip ${pkg.name} (no package.json)`);
      continue;
    }
    if (!pkgName.startsWith("@deepseek-ai/")) continue;
    const finalDest = join(nmDir, pkgName.split("/")[1]);
    await cp(src, finalDest, { recursive: true, force: true });
    console.log(`[install] deployed ${pkgName} -> ${finalDest}`);
  }

  // 移除已废弃的旧部署包(已被单插件取代)。
  for (const legacy of LEGACY_PKGS) {
    const p = join(nmDir, legacy);
    if (existsSync(p)) {
      await rm(p, { recursive: true, force: true });
      console.log(`[install] removed legacy deployed package ${legacy}`);
    }
  }

  // patch 更新(先备份)。
  if (!skipPatch) {
    if (existsSync(patchFile)) {
      const bak = `${patchFile}.bak-${new Date().toISOString().replace(/[:.]/g, "-")}`;
      writeFileSync(bak, readFileSync(patchFile, "utf8"));
      console.log(`[install] backed up cordis.patch.yml -> ${bak}`);
      patchCordisYml();
    } else {
      console.warn(`[install] WARN: ${patchFile} not found; patch update skipped`);
    }
  }

  if (withLaunchd) installLaunchd();

  console.log("[install] done. Restart DSH Desktop to load the plugin.");
  console.log("  restart 后日志应有: [memhop] started ... / [memhop] ready agent=<id> tools=31");
}

main().catch((err) => {
  console.error("[install] failed:", err);
  process.exit(1);
});
