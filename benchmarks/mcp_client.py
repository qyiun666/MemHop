#!/usr/bin/env python3
"""MemHop MCP Client — JSON-RPC 2.0 over stdio.

Usage:
    from benchmarks.mcp_client import MemHopMCPClient

    mcp = MemHopMCPClient(
        "target/release/memhop-mcp-server",
        "/tmp/bench.db",
        env_extra={"MEMHOP_ONNX_MODEL": "models/bge-m3"},
    )
    mcp.start_reader()
    for doc in docs:
        mcp.store(doc["text"], session_id=doc.get("session_id", "bench"))
    results = mcp.recall("query text", limit=10)
    mcp.close()
"""

import json
import os
import shutil
import subprocess
import time
from typing import Any, Optional


class MemHopMCPClient:
    """MCP JSON-RPC 2.0 client over stdio for MemHop benchmark."""

    def __init__(
        self,
        binary_path: str,
        db_path: str,
        env_extra: Optional[dict[str, str]] = None,
    ):
        if not os.path.exists(binary_path):
            raise FileNotFoundError(
                f"MCP server binary not found at {binary_path}. "
                "Build with: cargo build --release --features onnx"
            )
        self.binary = binary_path
        self.db_path = db_path
        self.env_extra = env_extra or {}
        self._proc: Optional[subprocess.Popen] = None
        self._req_id: int = 0

    # ── lifecycle ──────────────────────────────────────────

    def start_reader(self):
        """Start MCP server subprocess and initialize."""
        if self._proc is not None:
            return

        # Clean up old DB
        if os.path.exists(self.db_path):
            shutil.rmtree(self.db_path, ignore_errors=True)

        env = os.environ.copy()
        env["MEMHOP_DB_PATH"] = self.db_path
        env.update(self.env_extra)

        self._proc = subprocess.Popen(
            [self.binary],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=env,
            text=True,
        )

        # Initialize
        resp = self._call("initialize", {"protocolVersion": "2024-11-05"})
        if "error" in resp:
            raise RuntimeError(f"MCP initialize failed: {resp['error']}")

        # Send initialized notification
        self._notify("notifications/initialized", {})

    def close(self):
        """Stop MCP server and clean up database."""
        if self._proc is None:
            return

        try:
            self._proc.stdin.close()
            self._proc.stdout.close()
        except Exception:
            pass

        try:
            self._proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            self._proc.kill()
            self._proc.wait()

        self._proc = None

        # Clean DB
        if os.path.exists(self.db_path):
            shutil.rmtree(self.db_path, ignore_errors=True)

    # ── tools ──────────────────────────────────────────────

    def store(
        self,
        text: str,
        session_id: str = "bench",
        turn_id: str = "",
        turn_index: int = 0,
        topic_label: Optional[str] = None,
        valence: float = 0.0,
        arousal: float = 0.5,
    ) -> dict:
        """Store a perception/memory. Returns {"memory_id": "...", ...}."""
        args: dict[str, Any] = {
            "text": text,
            "session_id": session_id,
            "turn_id": turn_id,
            "turn_index": turn_index,
            "valence": valence,
            "arousal": arousal,
        }
        if topic_label:
            args["topic_label"] = topic_label
        return self._tool_call("memhop_store", args)

    def recall(
        self,
        query: str,
        session_id: str = "",
        limit: int = 10,
        max_tokens: Optional[int] = None,
    ) -> dict:
        """Recall memories. Returns {"results": [{id, text, kind, source}, ...]}."""
        args: dict[str, Any] = {
            "query": query,
            "session_id": session_id,
            "limit": limit,
        }
        if max_tokens is not None:
            args["max_tokens"] = max_tokens

        raw = self._tool_call("memhop_recall", args)

        # Unwrap MCP content wrapper
        items = self._unwrap_content(raw)

        return {"results": items}

    def dream(self) -> dict:
        """Run Dream consolidation. Returns {"consolidated_count": N, ...}."""
        raw = self._tool_call("memhop_dream", {})
        return self._unwrap_content(raw) or {}

    def stats(self) -> dict:
        """Get brain statistics."""
        return self._tool_call("memhop_stats", {})

    def count(self) -> int:
        """Get total engram count."""
        raw = self._tool_call("memhop_count", {})
        unwrapped = self._unwrap_content(raw)
        if isinstance(unwrapped, dict):
            return unwrapped.get("count", unwrapped.get("total", 0))
        return 0

    # ── helpers ────────────────────────────────────────────

    @staticmethod
    def _unwrap_content(result: Any) -> Any:
        """Unwrap MCP text content wrapper: {"content": [{"type":"text","text":"..."}]}."""
        if isinstance(result, dict) and "content" in result:
            for item in result["content"]:
                if isinstance(item, dict) and item.get("type") == "text":
                    try:
                        return json.loads(item["text"])
                    except (json.JSONDecodeError, TypeError):
                        return item["text"]
        return result

    # ── JSON-RPC internals ─────────────────────────────────

    def _next_id(self) -> int:
        self._req_id += 1
        return self._req_id

    def _call(self, method: str, params: dict) -> dict:
        req = {
            "jsonrpc": "2.0",
            "id": self._next_id(),
            "method": method,
            "params": params,
        }
        return self._send(req)

    def _notify(self, method: str, params: dict):
        req = {"jsonrpc": "2.0", "method": method, "params": params}
        self._send_raw(req)

    def _tool_call(self, name: str, arguments: dict) -> dict:
        return self._call("tools/call", {"name": name, "arguments": arguments})

    def _send(self, req: dict) -> dict:
        self._send_raw(req)
        return self._recv()

    def _send_raw(self, req: dict):
        if self._proc is None or self._proc.stdin is None:
            raise RuntimeError("MCP server not started")
        line = json.dumps(req, ensure_ascii=False)
        self._proc.stdin.write(line + "\n")
        self._proc.stdin.flush()

    def _recv(self) -> dict:
        if self._proc is None or self._proc.stdout is None:
            raise RuntimeError("MCP server not started")

        while True:
            line = self._proc.stdout.readline()
            if not line:
                stderr_output = ""
                if self._proc.stderr:
                    try:
                        stderr_output = self._proc.stderr.read()
                    except Exception:
                        pass
                raise RuntimeError(
                    f"MCP server closed stdout. stderr: {stderr_output[:500]}"
                )
            line = line.strip()
            if not line:
                continue
            try:
                resp = json.loads(line)
            except json.JSONDecodeError:
                continue
            if "error" in resp:
                raise RuntimeError(
                    f"MCP error {resp['error'].get('code')}: {resp['error'].get('message')}"
                )
            return resp.get("result", resp)
