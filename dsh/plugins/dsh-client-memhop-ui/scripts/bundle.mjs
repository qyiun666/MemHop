#!/usr/bin/env node
// scripts/bundle.mjs — 把 src/client/*.js 打包为 DSH client 插件格式
// （window.__ModuleLoader__.load({ id, factory })）。
//
// 打包方式：按依赖顺序把源码文件拼接进 factory 函数体（共享同一作用域），
// 注入 React 绑定。源码为 CJS 风格（顶层 const/function + module.exports），
// 不依赖任何构建工具链。

import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const srcDir = join(root, "src", "client");
const outFile = join(root, "lib", "client.js");

// 依赖顺序：theme → rpc → sections → Panel → SearchChip → index
const files = ["theme.js", "rpc.js", "sections.js", "Panel.js", "SearchChip.js", "index.js"];

const body = files
  .map((f) => {
    const src = readFileSync(join(srcDir, f), "utf8").trim();
    return `//#region src/client/${f}\n${src}\n//#endregion`;
  })
  .join("\n\n");

const out = `window.__ModuleLoader__.load({
	id: "@deepseek-ai/dsh-client-memhop-ui",
	factory: (require) => {
		var module = { exports: {} };
		var exports = module.exports;
		Object.defineProperty(exports, Symbol.toStringTag, { value: "Module" });
		let react = require("react");
		let react_jsx_runtime = require("react/jsx-runtime");
		/** React 绑定（源码用全局 React 名）。 */
		const React = react;

${body}

		return module.exports;
	}
});
`;

mkdirSync(join(root, "lib"), { recursive: true });
writeFileSync(outFile, out);
console.log(`[bundle] wrote ${outFile} (${out.length} bytes, ${files.length} files)`);
