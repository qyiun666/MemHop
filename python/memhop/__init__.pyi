"""Type stubs for memhop — SQLite for associative memory."""

from typing import Any, Optional


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

    def __init__(
        self,
        id: str,
        text: str,
        meta: Optional[dict[str, Any]] = ...,
        confidence: float = ...,
        created_at: str = ...,
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
    ) -> str: ...
    def recall(self, cue: str) -> Optional[Memory]: ...
    def recall_topk(self, cue: str, k: int = ...) -> list[Memory]: ...
    def forget(self, memory_id: str) -> bool: ...
    def update(
        self,
        memory_id: str,
        text: Optional[str] = ...,
        meta: Optional[dict[str, Any]] = ...,
    ) -> bool: ...
    def search(
        self,
        filters: dict[str, Any],
        limit: Optional[int] = ...,
    ) -> list[Memory]: ...
    def recent(self, limit: int = ...) -> list[Memory]: ...
    def remember_batch(self, items: list[dict[str, Any]]) -> list[str]: ...
    def purge_before(self, before_datetime: str) -> int: ...
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
