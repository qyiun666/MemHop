"""IDF (Inverse Document Frequency) builder for MemHop encoder.

Builds an IDF dictionary from a corpus of texts, which can be injected into
the n-gram encoder via ``db.set_encoder_idf(idf_dict)`` to downweight
common n-grams and emphasize rare/discriminative ones.

IDF formula::

    IDF(t) = ln((N + 1) / (DF(t) + 1)) + 1.0

Guarantees IDF >= 1.0 for all terms (no downweighting below uniform).
"""

from __future__ import annotations

import math
from collections import Counter
from typing import Dict, List


def build_idf(texts: List[str]) -> Dict[str, float]:
    """Build an IDF dictionary from a list of texts.

    Each text is treated as a bag of character n-grams (2/3/4-grams),
    matching the MemHop NgramEncoder's n-gram extraction scheme.

    Args:
        texts: Corpus of text strings to compute IDF from.

    Returns:
        Dictionary mapping n-gram string to its IDF value (float).
        All values are >= 1.0.

    Example:
        >>> idf_dict = build_idf(["hello world", "hello there"])
        >>> db.set_encoder_idf(idf_dict)
    """
    n = len(texts)
    if n == 0:
        return {}

    df: Counter[str] = Counter()  # document frequency per n-gram

    for text in texts:
        ngrams = _extract_ngrams(text)
        # Count unique n-grams per document (boolean presence)
        seen = set(ngrams)
        for ng in seen:
            df[ng] += 1

    idf: Dict[str, float] = {}
    for ngram, doc_freq in df.items():
        idf[ngram] = math.log((n + 1) / (doc_freq + 1)) + 1.0

    return idf


def _extract_ngrams(text: str) -> List[str]:
    """Extract character n-grams matching the Rust NgramEncoder logic.

    Returns 2/3/4-grams. Short texts (< 4 chars) also get unigram
    and whole-text augmentation.
    """
    chars = list(text)
    char_count = len(chars)
    result: List[str] = []

    if char_count > 0 and char_count < 4:
        result.append(text)
        for ch in chars:
            result.append(ch)

    for n in (2, 3, 4):
        if char_count < n:
            continue
        count = char_count - n + 1
        for i in range(count):
            result.append("".join(chars[i : i + n]))

    return result
