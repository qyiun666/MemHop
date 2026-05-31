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
import select
import shutil
import subprocess
import sys
import threading
import time
from typing import Any, Optional

# Default timeout for MCP server responses (seconds)
DEFAULT_RECV_TIMEOUT = 120


class MemHopMCPClient:
    """MCP JSON-RPC 2.0 client over stdio for MemHop benchmark.

    Reads stderr from the MCP server in a background thread so that
    ONNX / LMDB diagnostic messages are visible immediately instead
    of being buffered until the server exits.
    """

    def __init__(
        self,
        binary_path: str,
        db_path: str,
        env_extra: Optional[dict[str, str]] = None,
        recv_timeout: float = DEFAULT_RECV_TIMEOUT,
    ):
        if not os.path.exists(binary_path):
            raise FileNotFoundError(
                f"MCP server binary not found at {binary_path}. "
                "Build with: cargo build --release --features onnx"
            )
        self.binary = binary_path
        self.db_path = db_path
        self.env_extra = env_extra or {}
        self.recv_timeout = recv_timeout
        self._proc: Optional[subprocess.Popen] = None
        self._req_id: int = 0
        self._stderr_thread: Optional[threading.Thread] = None
        self._stderr_lines: list[str] = []
        self._stderr_done = threading.Event()

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
        # ort load-dynamic: force-set the dylib path on macOS
        if sys.platform == "darwin":
            current = env.get("ORT_DYLIB_PATH", "")
            if not current or not os.path.exists(current):
                for candidate in [
                    "/usr/local/lib/libonnxruntime.dylib",
                    "/opt/homebrew/lib/libonnxruntime.dylib",
                ]:
                    if os.path.exists(candidate):
                        env["ORT_DYLIB_PATH"] = candidate
                        break

        self._proc = subprocess.Popen(
            [self.binary],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=env,
            text=True,
        )

        # Start background stderr reader for diagnostics
        self._stderr_done.clear()
        self._stderr_lines.clear()
        self._stderr_thread = threading.Thread(
            target=self._read_stderr, daemon=True
        )
        self._stderr_thread.start()

        # Initialize
        print("  MCP: initializing...", end=" ", flush=True)
        resp = self._call("initialize", {"protocolVersion": "2024-11-05"})
        if "error" in resp:
            raise RuntimeError(f"MCP initialize failed: {resp['error']}")
        print("OK", flush=True)

        # Send initialized notification
        self._notify("notifications/initialized", {})
        print("  MCP: server ready", flush=True)

    def _read_stderr(self):
        """Background thread: read stderr lines for diagnostics."""
        try:
            for line in self._proc.stderr:
                line = line.rstrip()
                if line:
                    self._stderr_lines.append(line)
                    # Mirror to terminal so user sees ONNX loading progress
                    print(f"  [mcp] {line}", file=sys.stderr, flush=True)
        except Exception:
            pass
        finally:
            self._stderr_done.set()

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
        kind: str = "episode",
        tree_path: str = "",
        vector: Optional[list[float]] = None,
    ) -> dict:
        """Store a perception/memory.

        If `vector` is provided (list of floats), it's used directly.
        Otherwise the MCP server encodes from text via ONNX.

        Returns {"status": "stored", "engram_id": "...", ...}.
        """
        args: dict[str, Any] = {
            "text": text,
            "session_id": session_id,
            "turn_id": turn_id,
            "turn_index": turn_index,
            "valence": valence,
            "arousal": arousal,
            "kind": kind,
        }
        if topic_label:
            args["topic_label"] = topic_label
        if tree_path:
            args["tree_path"] = tree_path
        if vector:
            args["vector"] = vector
        result = self._tool_call("memhop_store", args)
        # Backward compat: alias engram_id as memory_id
        if isinstance(result, dict) and "engram_id" in result and "memory_id" not in result:
            result["memory_id"] = result["engram_id"]
        return result

    def store_knowledge(
        self,
        text: str,
        tree_path: str,
        source_path: str = "",
        source_textunit: str = "",
    ) -> dict:
        """Store a knowledge chunk (v0.11.0)."""
        args: dict[str, Any] = {
            "text": text,
            "kind": "knowledge",
            "tree_path": tree_path,
        }
        if source_path:
            args["source_path"] = source_path
        if source_textunit:
            args["source_textunit"] = source_textunit
        result = self._tool_call("memhop_store", args)
        if isinstance(result, dict) and "engram_id" in result and "memory_id" not in result:
            result["memory_id"] = result["engram_id"]
        return result

    def recall(
        self,
        query: str,
        session_id: str = "",
        limit: int = 10,
        mode: str = "retrieval",
        use_reranker: bool = True,
        kind_filter: Optional[list[str]] = None,
        tree: Optional[str] = None,
        query_vector: Optional[list[float]] = None,
    ) -> dict:
        """Recall memories (v0.11.0: unified episode + knowledge).

        Returns the raw MCP response dict with keys:
          "results" (list), "knowledge_memories" (list),
          "trace" (dict with latency_us, hopfield_candidates, spread_steps).
        """
        args: dict[str, Any] = {
            "query": query,
            "session_id": session_id,
            "limit": limit,
            "mode": mode,
            "use_reranker": use_reranker,
        }
        if kind_filter:
            args["kind_filter"] = kind_filter
        if tree:
            args["tree"] = tree
        if query_vector is not None:
            args["query_vector"] = query_vector

        return self._tool_call("memhop_recall", args)

    def dream(self) -> dict:
        """Run Dream consolidation (v0.11.0: includes knowledge processing).
        Returns {"status": "ok", "consolidated_count": N, "knowledge_processed": N, ...}.
        """
        return self._tool_call("memhop_dream", {}) or {}

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

        deadline = time.time() + self.recv_timeout
        while True:
            remaining = deadline - time.time()
            if remaining <= 0:
                recent_stderr = "\n".join(self._stderr_lines[-10:])
                raise RuntimeError(
                    f"MCP server timed out after {self.recv_timeout:.0f}s. "
                    f"Recent stderr:\n{recent_stderr}"
                )

            r, _, _ = select.select([self._proc.stdout], [], [], min(remaining, 5.0))
            if not r:
                continue

            line = self._proc.stdout.readline()
            if not line:
                self._stderr_done.wait(timeout=2)
                stderr_output = "\n".join(self._stderr_lines[-20:])
                raise RuntimeError(
                    f"MCP server closed stdout unexpectedly.\n"
                    f"Recent stderr:\n{stderr_output}"
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
