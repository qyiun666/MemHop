"""MemHop MCP client via stdio JSON-RPC.

Connects to memhop-mcp-server, used for:
- Performance benchmarks (latency, memory, throughput)
- NOT used for quality benchmarks (those use direct Rust binary with pre-encoded vectors)
"""

import subprocess
import json
import time
import threading
from typing import Optional
from queue import Queue, Empty


class MemHopMCPClient:
    """Stdio JSON-RPC client for memhop-mcp-server."""

    def __init__(self, server_path: str, db_path: str = "/tmp/memhop_bench.db"):
        """Start the MCP server process.

        Args:
            server_path: Path to compiled memhop-mcp-server binary.
            db_path: LMDB database path.
        """
        self.db_path = db_path
        self._counter = 0
        self._lock = threading.Lock()
        self._results: dict[int, Queue] = {}

        # Start server process
        env = {"MEMHOP_DB_PATH": db_path}
        self._proc = subprocess.Popen(
            [server_path],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            env=env,
            bufsize=1,
        )

        # Initialize MCP
        self._call("initialize", {"protocolVersion": "2024-11-05"})
        self._send_notification("notifications/initialized", {})

    def _send_notification(self, method: str, params: dict):
        """Send a JSON-RPC notification (no response expected)."""
        msg = json.dumps({"jsonrpc": "2.0", "method": method, "params": params})
        try:
            self._proc.stdin.write(msg + "\n")
            self._proc.stdin.flush()
        except (BrokenPipeError, OSError):
            pass

    def _call(self, method: str, params: dict, timeout: float = 60.0) -> dict:
        """Send a JSON-RPC request and wait for the response."""
        with self._lock:
            self._counter += 1
            req_id = self._counter
            q: Queue = Queue()
            self._results[req_id] = q

        request = json.dumps({
            "jsonrpc": "2.0",
            "id": req_id,
            "method": method,
            "params": params,
        })

        try:
            self._proc.stdin.write(request + "\n")
            self._proc.stdin.flush()
        except (BrokenPipeError, OSError) as e:
            with self._lock:
                self._results.pop(req_id, None)
            raise ConnectionError(f"MCP server connection lost: {e}")

        try:
            result = q.get(timeout=timeout)
            with self._lock:
                self._results.pop(req_id, None)
            return result
        except Empty:
            with self._lock:
                self._results.pop(req_id, None)
            raise TimeoutError(f"MCP call '{method}' timed out after {timeout}s")

    def start_reader(self):
        """Start background thread to read responses from stdout."""
        def _read():
            for line in self._proc.stdout:
                line = line.strip()
                if not line:
                    continue
                try:
                    msg = json.loads(line)
                except json.JSONDecodeError:
                    continue
                rid = msg.get("id")
                if rid is not None:
                    with self._lock:
                        q = self._results.get(rid)
                    if q:
                        q.put(msg)
        t = threading.Thread(target=_read, daemon=True)
        t.start()
        return t

    # ── MCP Tool Wrappers ──────────────────────────────────

    def store(self, text: str, session_id: str = "bench",
              valence: float = 0.0, arousal: float = 0.5) -> dict:
        """Store a memory."""
        return self._tool_call("memhop_store", {
            "text": text,
            "session_id": session_id,
            "valence": valence,
            "arousal": arousal,
        })

    def recall(self, query: str, session_id: str = "bench",
               limit: int = 10, query_vector: Optional[list] = None) -> dict:
        """Recall memories."""
        args = {
            "query": query,
            "session_id": session_id,
            "limit": limit,
        }
        if query_vector is not None:
            args["query_vector"] = query_vector
        return self._tool_call("memhop_recall", args)

    def dream(self) -> dict:
        """Run dream consolidation."""
        return self._tool_call("memhop_dream", {})

    def stats(self) -> dict:
        """Get brain statistics."""
        return self._tool_call("memhop_stats", {})

    def count(self) -> int:
        """Get total engram count."""
        result = self._tool_call("memhop_count", {})
        return result.get("count", 0)

    def _tool_call(self, name: str, arguments: dict) -> dict:
        result = self._call("tools/call", {
            "name": name,
            "arguments": arguments,
        })
        if "error" in result:
            raise RuntimeError(f"MCP tool '{name}' error: {result['error']}")
        return result.get("result", {})

    def close(self):
        """Stop the MCP server."""
        try:
            self._proc.stdin.close()
            self._proc.stdout.close()
            self._proc.stderr.close()
            self._proc.terminate()
            self._proc.wait(timeout=5)
        except Exception:
            self._proc.kill()
