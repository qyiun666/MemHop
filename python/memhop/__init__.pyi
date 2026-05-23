"""Type stubs for memhop — SQLite for associative memory (v0.5.0)."""

from __future__ import annotations

from typing import Any, Callable, Optional


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
        gating_enabled: bool = ...,
        gating_threshold: float = ...,
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

    # ── v0.4.0: Plasticity API ──

    def recall_with_plasticity(
        self,
        cue: str,
        *,
        include_blob: bool = ...,
    ) -> Optional[Memory]: ...

    def enable_plasticity(self, enabled: bool) -> None: ...

    def set_plasticity_config(
        self,
        min_drift_attention: float = ...,
        discrimination_threshold: float = ...,
        reinforce_rate: float = ...,
        discriminate_rate: float = ...,
        decay_threshold_days: int = ...,
        decay_rate: float = ...,
    ) -> None: ...

    def get_memory_stats(self, memory_id: str) -> Optional[dict[str, int]]: ...

    def trigger_decay(self) -> int: ...

    # ── v0.4.0: Encoder IDF API ──

    def set_encoder_idf(self, idf_dict: dict[str, float]) -> None: ...
    def clear_encoder_idf(self) -> None: ...

    # ── v0.4.0: Scene Gating API ──

    def set_gating(self, enabled: bool) -> None: ...
    def set_gating_threshold(self, threshold: float) -> None: ...
    def reset_scene(self) -> None: ...

    # ── v0.5.3: Spread activation ──

    def spread_activation(
        self,
        cue: str,
        *,
        max_hops: int = ...,
    ) -> list[dict[str, Any]]: ...

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


# ── v0.5.0 BrainLoop types ──


class BrainConfig:
    """BrainLoop configuration."""

    max_attempts: int
    confidence_threshold: float
    compress_threshold: int
    auto_consolidate: bool
    scene_aware: bool
    plasticity_enabled: bool
    calibrate_threshold: int
    fast_path_threshold: float

    def __init__(
        self,
        max_attempts: int = ...,
        confidence_threshold: float = ...,
        compress_threshold: int = ...,
        auto_consolidate: bool = ...,
        scene_aware: bool = ...,
        plasticity_enabled: bool = ...,
        calibrate_threshold: int = ...,
        fast_path_threshold: float = ...,
    ) -> None: ...
    def __repr__(self) -> str: ...


class HttpThinker:
    """OpenAI-compatible HTTP LLM provider."""

    endpoint: str
    model: str
    fast_model: str

    def __init__(
        self,
        endpoint: str = ...,
        api_key: str = ...,
        model: str = ...,
        fast_model: str = ...,
    ) -> None: ...
    def __repr__(self) -> str: ...


class FastReflex:
    """Rule-based reflex system."""

    def __init__(self) -> None: ...
    def __repr__(self) -> str: ...
    def add_rule(self, pattern: str, response: str) -> None: ...


class CognitionHealth:
    """Cognitive health metrics for each turn."""

    @property
    def llm_calls(self) -> int: ...
    @property
    def tokens_used(self) -> int: ...
    @property
    def total_memories(self) -> int: ...
    @property
    def avg_confidence(self) -> float: ...
    @property
    def strategy_hint(self) -> Optional[str]: ...


class BrainNotifications:
    """Meta-notifications from brain to body."""

    @property
    def new_knowledge_count(self) -> int: ...
    @property
    def compression_triggered(self) -> bool: ...
    @property
    def cognition_health(self) -> CognitionHealth: ...


class BodyAction:
    """Actions the body must perform."""

    @property
    def action_type(self) -> str: ...
    @property
    def name(self) -> Optional[str]: ...
    @property
    def params(self) -> Optional[str]: ...
    @property
    def question(self) -> Optional[str]: ...
    @property
    def options(self) -> Optional[list[str]]: ...
    @property
    def danger_level(self) -> Optional[str]: ...
    @property
    def prompt(self) -> Optional[str]: ...
    @property
    def path(self) -> Optional[str]: ...


class BodyResult:
    """Output from body actions fed back into brain."""

    source: str
    text: str
    meta: dict[str, str]

    def __init__(self, source: str = ..., text: str = ..., meta: Optional[dict[str, str]] = ...) -> None: ...


class BrainAction:
    """Output from BrainLoop cognitive cycle."""

    @property
    def action_type(self) -> str: ...
    @property
    def chunk(self) -> Optional[str]: ...
    @property
    def actions(self) -> Optional[list[BodyAction]]: ...
    @property
    def context(self) -> Optional[str]: ...
    @property
    def for_user(self) -> Optional[str]: ...
    @property
    def notifications(self) -> Optional[BrainNotifications]: ...


class BrainLoop:
    """Complete cognitive loop — MeowHop brain."""

    def __init__(
        self,
        db_path: Optional[str] = ...,
        thinker: Optional[HttpThinker] = ...,
        cerebellum: Optional[FastReflex] = ...,
        config: Optional[BrainConfig] = ...,
    ) -> None: ...

    def process(self, user_input: str) -> BrainAction: ...
    def process_streaming(self, user_input: str, on_chunk: Callable[[str], None]) -> BrainAction: ...
    def feed_body_result(self, results: list[BodyResult]) -> BrainAction: ...
    def __repr__(self) -> str: ...


# ── Top-level ──


def open(
    path: str = ...,
    *,
    encoder: str = ...,
    confidence_threshold: float = ...,
    beta: float = ...,
    max_memories: int = ...,
    timezone: str = ...,
    gating_enabled: bool = ...,
    gating_threshold: float = ...,
) -> MemHopEngine: ...


__version__: str
__all__: list[str]
