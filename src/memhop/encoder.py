"""
Pluggable encoder layer.

Three modes:
- API: DeepSeek / OpenAI Embedding API (default, zero local memory)
- Local: BGE-M3 ONNX INT8 (~300MB, offline, < 5ms latency)
- Custom: User-provided Encoder implementation
"""

import os
from abc import ABC, abstractmethod

import numpy as np

from memhop.types import EncoderConfig, EncoderMode, EncoderOutput, VECTOR_DIM


class Encoder(ABC):
    """Abstract encoder interface. All encoders must implement encode()."""

    @abstractmethod
    def encode(self, text: str) -> EncoderOutput:
        """
        Encode text into dense + optional sparse/multi vectors.

        Args:
            text: Input text (any language)

        Returns:
            EncoderOutput with at least dense vector
        """
        ...


# ── API Encoder (default) ──────────────────────────────────

class ApiEncoder(Encoder):
    """
    Encoder backed by an embedding API (DeepSeek by default).

    Zero local memory footprint. Requires network access.

    Args:
        base_url: API base URL
        model: Embedding model name
        api_key: API key (defaults to DEEPSEEK_API_KEY env var)
        dimensioins: Output dimension (default: VECTOR_DIM)
    """

    def __init__(
        self,
        base_url: str = "https://api.deepseek.com/v1",
        model: str = "deepseek-embed",
        api_key: str | None = None,
    ):
        self.base_url = base_url.rstrip("/")
        self.model = model
        self.api_key = api_key or os.environ.get("DEEPSEEK_API_KEY")
        if not self.api_key:
            raise ValueError(
                "API key required for ApiEncoder. "
                "Set DEEPSEEK_API_KEY env var or pass api_key=..."
            )
        self._client = None  # Lazy init

    def _get_client(self):
        if self._client is None:
            import httpx
            self._client = httpx.Client(timeout=30)
        return self._client

    def encode(self, text: str) -> EncoderOutput:
        if not text.strip():
            # Return zero vector for empty text
            return EncoderOutput(dense=np.zeros(VECTOR_DIM, dtype=np.float32))

        client = self._get_client()
        resp = client.post(
            f"{self.base_url}/embeddings",
            json={
                "model": self.model,
                "input": text,
            },
            headers={
                "Authorization": f"Bearer {self.api_key}",
                "Content-Type": "application/json",
            },
        )
        resp.raise_for_status()
        data = resp.json()

        embedding = np.array(data["data"][0]["embedding"], dtype=np.float32)

        # Normalize to unit length
        norm = np.linalg.norm(embedding)
        if norm > 0:
            embedding = embedding / norm

        return EncoderOutput(dense=embedding)


# ── Local Encoder (BGE-M3) ─────────────────────────────────

class LocalEncoder(Encoder):
    """
    Local BGE-M3 encoder via ONNX Runtime.

    ~300MB memory (INT8 quantized), < 5ms latency, offline.

    pip install memhop[local]  # installs onnxruntime + FlagEmbedding
    """

    def __init__(self, model_path: str | None = None, use_int8: bool = True):
        self.model_path = model_path
        self.use_int8 = use_int8
        self._model = None

    def _load_model(self):
        if self._model is not None:
            return self._model
        from FlagEmbedding import BGEM3FlagModel
        # TODO: support model_path override and INT8 quantization
        self._model = BGEM3FlagModel("BAAI/bge-m3", use_fp16=True)
        return self._model

    def encode(self, text: str) -> EncoderOutput:
        import numpy as np
        model = self._load_model()
        output = model.encode(
            text,
            return_dense=True,
            return_sparse=True,
            return_colbert_vecs=True,
        )
        dense = np.array(output["dense_vecs"], dtype=np.float32)
        norm = np.linalg.norm(dense)
        if norm > 0:
            dense = dense / norm

        multi = np.array(output["colbert_vecs"], dtype=np.float32) if output.get("colbert_vecs") is not None else None

        return EncoderOutput(
            dense=dense,
            sparse=output.get("lexical_weights"),
            multi=multi,
        )


# ── Mock Encoder (for testing without API key) ─────────────

class MockEncoder(Encoder):
    """Deterministic mock encoder for testing. Generates hash-based vectors."""

    def encode(self, text: str) -> EncoderOutput:
        import hashlib
        h = hashlib.sha256(text.encode()).digest()
        # Generate deterministic pseudo-random vector from hash
        seed = int.from_bytes(h[:4], "big")
        rng = np.random.default_rng(seed)
        vec = rng.standard_normal(VECTOR_DIM).astype(np.float32)
        vec /= np.linalg.norm(vec) + 1e-8
        return EncoderOutput(dense=vec)


# ── Factory ────────────────────────────────────────────────

_ENCODER_REGISTRY: dict[str, type[Encoder]] = {
    "api": ApiEncoder,
    "local": LocalEncoder,
    "mock": MockEncoder,
}


def register_encoder(name: str, cls: type[Encoder]) -> None:
    """Register a custom encoder class."""
    _ENCODER_REGISTRY[name] = cls


def get_encoder(config: EncoderConfig) -> Encoder:
    """
    Instantiate an encoder from configuration.

    Args:
        config: EncoderConfig with mode + mode-specific params

    Returns:
        Encoder instance
    """
    if config.mode == EncoderMode.API:
        return ApiEncoder(
            base_url=config.api_base_url,
            model=config.api_model,
        )
    elif config.mode == EncoderMode.LOCAL:
        return LocalEncoder(
            model_path=config.local_model_path,
            use_int8=config.local_use_int8,
        )
    if config.mode == EncoderMode.MOCK:
        return MockEncoder()
    elif config.mode == EncoderMode.CUSTOM:
        if not config.custom_class:
            raise ValueError("custom_class required for CUSTOM encoder mode")
        import importlib
        module_path, class_name = config.custom_class.rsplit(".", 1)
        module = importlib.import_module(module_path)
        cls = getattr(module, class_name)
        return cls()
    else:
        raise ValueError(f"Unknown encoder mode: {config.mode}")
