"""Type stubs for memhop — SQLite for associative memory."""

from typing import Any, Optional, Literal


class Memory:
    """A single recalled memory, returned by recall() / recall_topk() / search() / recent()."""

    @property
    def id(self) -> str: ...
    @property
    def text(self) -> str: ...
    @property
    def meta(self) -> dict[str, Any]: ...
    @property
    def confidence(self) -> float: ...
    @property
    def created_at(self) -> str: ...
    @property
    def content_type(self) -> Optional[str]: ...
    @property
    def blob(self) -> Optional[bytes]: ...

    def __init__(
        self,
        id: str,
        text: str,
        meta: Optional[dict[str, Any]] = ...,
        confidence: float = ...,
        created_at: str = ...,
        content_type: Optional[str] = ...,
        blob: Optional[bytes] = ...,
    ) -> None: ...
    def __repr__(self) -> str: ...


class MemHopError(Exception): ...
class MemHopClosedError(MemHopError): ...


class MemHopEngine:
    """MemHop associative memory engine — the main entry point."""

    def __init__(
        self,
        path: str = ...,
        *,
        encoder: str = ...,
        confidence_threshold: float = ...,
        beta: float = ...,
        max_memories: int = ...,
        timezone: str = ...,
    ) -> None: ...

    def remember(
        self,
        text: str,
        meta: Optional[dict[str, Any]] = ...,
        memory_id: Optional[str] = ...,
        content_type: Optional[str] = ...,
        blob: Optional[bytes] = ...,
    ) -> str: ...

    def recall(
        self,
        cue: str,
        *,
        include_blob: bool = ...,
        scope: Optional[dict[str, Any]] = ...,
        time_alpha: float = ...,
        importance_alpha: float = ...,
    ) -> Optional[Memory]: ...

    def recall_topk(
        self,
        cue: str,
        k: int = ...,
        *,
        include_blob: bool = ...,
        scope: Optional[dict[str, Any]] = ...,
        time_alpha: float = ...,
        importance_alpha: float = ...,
    ) -> list[Memory]: ...

    def fuse_recall(
        self,
        cues: list[str],
        *,
        weights: Optional[list[float]] = ...,
        include_blob: bool = ...,
        scope: Optional[dict[str, Any]] = ...,
        time_alpha: float = ...,
        importance_alpha: float = ...,
    ) -> Optional[Memory]: ...

    def fuse_recall_topk(
        self,
        cues: list[str],
        k: int = ...,
        *,
        weights: Optional[list[float]] = ...,
        include_blob: bool = ...,
        scope: Optional[dict[str, Any]] = ...,
        time_alpha: float = ...,
        importance_alpha: float = ...,
    ) -> list[Memory]: ...

    def forget(self, memory_id: str) -> bool: ...

    def update(
        self,
        memory_id: str,
        text: Optional[str] = ...,
        meta: Optional[dict[str, Any]] = ...,
        content_type: Optional[str] = ...,
        blob: Optional[bytes] = ...,
    ) -> bool: ...

    def search(
        self,
        filters: dict[str, Any],
        limit: Optional[int] = ...,
    ) -> list[Memory]: ...

    def recent(self, limit: int = ...) -> list[Memory]: ...

    def remember_batch(self, items: list[dict[str, Any]]) -> list[str]: ...

    def purge_before(self, before_datetime: str) -> int: ...

    # ── v0.3.0: cross-layer links ──

    def link_to(
        self,
        from_id: str,
        to_id: str,
        link_type: str = ...,
    ) -> bool: ...

    def links_of(self, memory_id: str) -> list[dict[str, Any]]: ...
    def links_to(self, memory_id: str) -> list[dict[str, Any]]: ...

    # ── v0.3.0: visualization ──

    def entity_graph(self) -> dict[str, Any]: ...
    def knowledge_tree(self) -> list[dict[str, Any]]: ...

    def episode_thread(
        self,
        session_id: Optional[str] = ...,
        layer: Optional[str] = ...,
        limit: int = ...,
    ) -> list[Memory]: ...

    def memories_by_layer(
        self,
        layer: Optional[str] = ...,
    ) -> dict[str, list[dict[str, Any]]]: ...

    def close(self) -> None: ...

    @property
    def count(self) -> int: ...
    @property
    def stats(self) -> dict[str, Any]: ...

    def __enter__(self) -> MemHopEngine: ...
    def __exit__(
        self,
        exc_type: Any,
        exc_val: Any,
        exc_tb: Any,
    ) -> None: ...
    def __repr__(self) -> str: ...


def open(
    path: str = ...,
    *,
    encoder: str = ...,
    confidence_threshold: float = ...,
    beta: float = ...,
    max_memories: int = ...,
    timezone: str = ...,
) -> MemHopEngine: ...


__version__: str
__all__: list[str]