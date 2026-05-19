"""
LMDB-backed persistent storage.

Three logical databases within a single env file (memhop.db):

    patterns_db:  memory_id → float32 embedding vector (raw bytes)
    blobs_db:     memory_id → zstd-compressed text + meta (JSON)
    meta_db:      memory_id → timestamp + confidence (binary struct)
"""

import json
import struct
import time
import zlib
from pathlib import Path
from typing import Any

import lmdb
import numpy as np

from memhop.types import Memory, VECTOR_DIM

# ── LMDB sub-database names ────────────────────────────────
DB_PATTERNS = b"p"
DB_BLOBS = b"b"
DB_META = b"m"

# macOS doesn't support writemap
LMDB_FLAGS = {"writemap": False, "map_async": False, "metasync": True}


class LmdbStorage:
    """
    LMDB single-file persistent storage backend.

    Opens three sub-databases within one LMDB environment:
    - patterns: raw float32 embedding vectors
    - blobs: compressed text + metadata
    - meta: lightweight index (timestamp, confidence)

    Args:
        path: Database file path (default: memhop.db)
        max_memories: Soft upper bound for stored memories
        map_size: Initial mmap size in bytes (default: 1GB)
    """

    def __init__(
        self,
        path: str = "memhop.db",
        max_memories: int = 1_000_000,
        map_size: int = 1024 * 1024 * 1024,
    ):
        self.path = Path(path)
        self.max_memories = max_memories
        self.path.parent.mkdir(parents=True, exist_ok=True)

        # Open LMDB environment
        self._env = lmdb.open(
            str(self.path),
            map_size=map_size,
            max_dbs=3,
            **LMDB_FLAGS,
        )

        # Open sub-databases at env level (required by LMDB Python binding)
        self._patterns_db = self._env.open_db(DB_PATTERNS, create=True)
        self._blobs_db = self._env.open_db(DB_BLOBS, create=True)
        self._meta_db = self._env.open_db(DB_META, create=True)

    # ── Transactions ────────────────────────────────────────

    def _txn(self, write: bool = False):
        return self._env.begin(write=write)

    def close(self) -> None:
        self._env.close()

    def sync(self) -> None:
        self._env.sync()

    # ── Read ────────────────────────────────────────────────

    def get_memory(self, memory_id: str) -> Memory | None:
        """Retrieve a full memory by ID."""
        key = memory_id.encode()
        with self._txn() as txn:
            blob_data = txn.get(key, db=self._blobs_db)
            if blob_data is None:
                return None

            text, meta = _deserialize_blob(blob_data)

            meta_bin = txn.get(key, db=self._meta_db)
            confidence = _deserialize_confidence(meta_bin) if meta_bin else 0.0

        return Memory(id=memory_id, text=text, meta=meta or {}, confidence=confidence)

    def get_pattern(self, memory_id: str) -> np.ndarray | None:
        """Get the embedding vector for a memory."""
        key = memory_id.encode()
        with self._txn() as txn:
            data = txn.get(key, db=self._patterns_db)
            if data is None:
                return None
            return np.frombuffer(data, dtype=np.float32).copy()

    def list_ids(self) -> list[str]:
        """List all memory IDs."""
        with self._txn() as txn:
            cursor = txn.cursor(db=self._blobs_db)
            ids = [key.decode() for key, _ in cursor]
            return ids

    def count(self) -> int:
        """Count total memories."""
        with self._txn() as txn:
            return txn.stat(db=self._blobs_db)["entries"]

    # ── Write ───────────────────────────────────────────────

    def put_memory(
        self,
        memory_id: str,
        text: str,
        meta: dict[str, Any] | None = None,
        vector: np.ndarray | None = None,
    ) -> None:
        """Store a complete memory (text + meta + vector)."""
        key = memory_id.encode()
        blob_data = _serialize_blob(text, meta or {})

        with self._txn(write=True) as txn:
            txn.put(key, blob_data, db=self._blobs_db)
            txn.put(key, _serialize_meta(timestamp=time.time()), db=self._meta_db)

            if vector is not None:
                txn.put(
                    key,
                    np.asarray(vector, dtype=np.float32).tobytes(),
                    db=self._patterns_db,
                )

    def put_pattern(self, memory_id: str, vector: np.ndarray) -> None:
        """Store or update just the embedding vector."""
        key = memory_id.encode()
        with self._txn(write=True) as txn:
            txn.put(
                key,
                np.asarray(vector, dtype=np.float32).tobytes(),
                db=self._patterns_db,
            )

    def update_meta(self, memory_id: str, meta: dict[str, Any]) -> None:
        """Update metadata for an existing memory (merge)."""
        key = memory_id.encode()
        with self._txn(write=True) as txn:
            old = txn.get(key, db=self._blobs_db)
            if old is None:
                return
            text, old_meta = _deserialize_blob(old)
            old_meta.update(meta)
            txn.put(key, _serialize_blob(text, old_meta), db=self._blobs_db)

    def delete_memory(self, memory_id: str) -> bool:
        """Delete a memory and all its data. Returns True if deleted."""
        key = memory_id.encode()
        with self._txn(write=True) as txn:
            if txn.get(key, db=self._blobs_db) is None:
                return False
            txn.delete(key, db=self._blobs_db)
            txn.delete(key, db=self._meta_db)
            txn.delete(key, db=self._patterns_db)
        return True

    # ── Bulk ────────────────────────────────────────────────

    def load_all_patterns(self) -> dict[str, np.ndarray]:
        """Load all patterns as {id: vector}. Used at engine startup."""
        patterns = {}
        with self._txn() as txn:
            cursor = txn.cursor(db=self._patterns_db)
            for key, data in cursor:
                patterns[key.decode()] = np.frombuffer(data, dtype=np.float32).copy()
        return patterns


# ── Serialization helpers ──────────────────────────────────

def _serialize_blob(text: str, meta: dict[str, Any]) -> bytes:
    payload = json.dumps({"t": text, "m": meta}, ensure_ascii=False)
    return zlib.compress(payload.encode("utf-8"))


def _deserialize_blob(data: bytes) -> tuple[str, dict[str, Any]]:
    payload = json.loads(zlib.decompress(data).decode("utf-8"))
    return payload["t"], payload.get("m", {})


def _serialize_meta(timestamp: float, confidence: float = 0.0) -> bytes:
    return struct.pack(">df", timestamp, confidence)


def _deserialize_confidence(data: bytes) -> float:
    try:
        _, conf = struct.unpack(">df", data)
        return float(conf)
    except struct.error:
        return 0.0
