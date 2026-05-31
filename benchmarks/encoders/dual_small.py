"""DualSmallEncoder: BGE-small-zh + MiniLM-L6 dual encoder.

Chinese text -> zh_encoder (512d)  -> tree="zh"
English text -> en_encoder (384d) -> tree="en"

Two small models total ~280MB (1/9 of BGE-M3's 2.5GB).
"""

import os
from typing import Any

os.environ["TOKENIZERS_PARALLELISM"] = "false"


class DualSmallEncoder:
    """BGE-small-zh + MiniLM-L6 dual encoder for Chinese/English text."""

    def __init__(self, device: str = "cpu") -> None:
        """Load both models. Each model loads only once."""
        try:
            from sentence_transformers import SentenceTransformer
        except ImportError:
            raise ImportError(
                "sentence-transformers is required for DualSmallEncoder.\n"
                "Install it with: pip install sentence-transformers"
            )
        self._device = device
        self._zh_model = SentenceTransformer(
            "BAAI/bge-small-zh-v1.5",
            device=device,
        )
        self._en_model = SentenceTransformer(
            "sentence-transformers/all-MiniLM-L6-v2",
            device=device,
        )

    def encode(self, text: str) -> tuple[list[float], str]:
        """Encode a single text and return (vector, tree_name).

        Chinese text -> zh (512d), English text -> en (384d).
        """
        if not text:
            return [], "en"

        if self._is_cjk(text):
            emb = self._zh_model.encode(text, normalize_embeddings=True)
            return emb.tolist(), "zh"
        else:
            emb = self._en_model.encode(text, normalize_embeddings=True)
            return emb.tolist(), "en"

    def encode_many(self, texts: list[str]) -> list[tuple[list[float], str]]:
        """Encode multiple texts and return [(vector, tree_name), ...]."""
        if not texts:
            return []

        zh_texts: list[str] = []
        en_texts: list[str] = []
        zh_indices: list[int] = []
        en_indices: list[int] = []

        for i, t in enumerate(texts):
            if t and self._is_cjk(t):
                zh_texts.append(t)
                zh_indices.append(i)
            else:
                en_texts.append(t)
                en_indices.append(i)

        placeholders = [([], "") for _ in texts]

        if zh_texts:
            zh_embs = self._zh_model.encode(zh_texts, normalize_embeddings=True)
            for idx, emb in zip(zh_indices, zh_embs):
                placeholders[idx] = (emb.tolist(), "zh")

        if en_texts:
            en_embs = self._en_model.encode(en_texts, normalize_embeddings=True)
            for idx, emb in zip(en_indices, en_embs):
                placeholders[idx] = (emb.tolist(), "en")

        return placeholders

    @staticmethod
    def _is_cjk(text: str) -> bool:
        """Determine whether text is primarily Chinese.

        CJK character ratio > 30% -> True (Chinese), otherwise False (English).
        """
        if not text:
            return False
        cjk_count = sum(
            1
            for c in text
            if "\u4e00" <= c <= "\u9fff" or "\u3400" <= c <= "\u4dbf"
        )
        return cjk_count / max(len(text), 1) > 0.3

    @property
    def name(self) -> str:
        return "dual-small"

    @property
    def info(self) -> dict[str, Any]:
        """Return encoder metadata."""
        return {
            "model_id": "BAAI/bge-small-zh-v1.5",
            "alt_model_id": "sentence-transformers/all-MiniLM-L6-v2",
            "zh_dim": 512,
            "en_dim": 384,
            "device": self._device,
            "source": "python",
        }
