"""Standard IR evaluation metrics.

Implements: NDCG@k, MRR, Recall@k, Precision@k.

All metrics follow the conventions used by MTEB / BEIR / TREC
for direct comparability with published results.
"""

import math
import numpy as np
from typing import Sequence


def ndcg_at_k(
    ranked_ids: Sequence[str],
    relevant: dict[str, int],
    k: int = 10,
) -> float:
    """Normalized Discounted Cumulative Gain at k.

    Args:
        ranked_ids: Ordered list of retrieved document IDs.
        relevant: {doc_id: relevance_score}. Binary (0/1) or graded.
        k: Cutoff rank.

    Returns:
        NDCG@k score in [0, 1].
    """
    cutoff = min(len(ranked_ids), k)
    if cutoff == 0:
        return 0.0

    # DCG
    dcg = 0.0
    for pos, doc_id in enumerate(ranked_ids[:cutoff]):
        rel = relevant.get(doc_id, 0)
        if pos == 0:
            dcg += rel
        else:
            dcg += rel / math.log2(pos + 2)  # pos+2 because pos is 0-indexed

    # IDCG: ideal ordering (all relevant docs sorted by relevance, descending)
    ideal_rels = sorted(relevant.values(), reverse=True)[:cutoff]
    idcg = 0.0
    for pos, rel in enumerate(ideal_rels):
        if pos == 0:
            idcg += rel
        else:
            idcg += rel / math.log2(pos + 2)

    if idcg == 0.0:
        return 0.0
    return dcg / idcg


def mrr(
    ranked_ids: Sequence[str],
    relevant: dict[str, int],
) -> float:
    """Mean Reciprocal Rank.

    Args:
        ranked_ids: Ordered list of retrieved document IDs.
        relevant: {doc_id: relevance_score}. Any score > 0 means relevant.

    Returns:
        MRR score in [0, 1].
    """
    for pos, doc_id in enumerate(ranked_ids):
        if relevant.get(doc_id, 0) > 0:
            return 1.0 / (pos + 1)
    return 0.0


def recall_at_k(
    ranked_ids: Sequence[str],
    relevant: dict[str, int],
    k: int = 10,
) -> float:
    """Recall at k.

    Args:
        ranked_ids: Ordered list of retrieved document IDs.
        relevant: {doc_id: relevance_score}.
        k: Cutoff rank.

    Returns:
        Recall@k in [0, 1].
    """
    total_relevant = sum(1 for v in relevant.values() if v > 0)
    if total_relevant == 0:
        return 0.0
    found = sum(1 for doc_id in ranked_ids[:k] if relevant.get(doc_id, 0) > 0)
    return found / total_relevant


def precision_at_k(
    ranked_ids: Sequence[str],
    relevant: dict[str, int],
    k: int = 10,
) -> float:
    """Precision at k.

    Args:
        ranked_ids: Ordered list of retrieved document IDs.
        relevant: {doc_id: relevance_score}.
        k: Cutoff rank.

    Returns:
        Precision@k in [0, 1].
    """
    if k == 0:
        return 0.0
    found = sum(1 for doc_id in ranked_ids[:k] if relevant.get(doc_id, 0) > 0)
    return found / k


def evaluate_all(
    ranked_ids: Sequence[str],
    relevant: dict[str, int],
    k_values: list[int] = [1, 5, 10],
) -> dict:
    """Compute all standard IR metrics for a single query.

    Returns dict with keys: ndcg_10, mrr, recall_1, recall_5, recall_10, precision_10.
    """
    return {
        "ndcg_10": ndcg_at_k(ranked_ids, relevant, k=10),
        "mrr": mrr(ranked_ids, relevant),
        **{f"recall_{k}": recall_at_k(ranked_ids, relevant, k=k) for k in k_values},
        "precision_10": precision_at_k(ranked_ids, relevant, k=10),
    }


def aggregate_metrics(
    rankings: dict[str, list[str]],     # {query_id: [doc_id, ...]}
    qrels: dict[str, dict[str, int]],   # {query_id: {doc_id: relevance}}
) -> dict:
    """Average metrics over all queries.

    Returns dict with mean and std for each metric.
    """
    ndcg_scores = []
    mrr_scores = []
    r1_scores = []
    r5_scores = []
    r10_scores = []
    p10_scores = []

    for qid, ranked in rankings.items():
        rel = qrels.get(qid, {})
        ndcg_scores.append(ndcg_at_k(ranked, rel, k=10))
        mrr_scores.append(mrr(ranked, rel))
        r1_scores.append(recall_at_k(ranked, rel, k=1))
        r5_scores.append(recall_at_k(ranked, rel, k=5))
        r10_scores.append(recall_at_k(ranked, rel, k=10))
        p10_scores.append(precision_at_k(ranked, rel, k=10))

    def stats(scores: list[float]) -> dict:
        arr = np.array(scores)
        return {"mean": float(np.mean(arr)), "std": float(np.std(arr, ddof=1)) if len(arr) > 1 else 0.0}

    return {
        "ndcg_10": stats(ndcg_scores),
        "mrr": stats(mrr_scores),
        "recall_1": stats(r1_scores),
        "recall_5": stats(r5_scores),
        "recall_10": stats(r10_scores),
        "precision_10": stats(p10_scores),
        "num_queries": len(rankings),
    }


# ── LoCoMo-style F1 metrics ───────────────────────────────


def locomo_f1_at_k(
    recalled_texts: list[str],
    answer_text: str,
    k: int = 10,
) -> float:
    """LoCoMo-style F1: answer text contained in any of the top-K recalled texts.

    Returns 1.0 if a match is found, 0.0 otherwise.
    Match = answer text (case-insensitive) is a substring of any recalled text.
    """
    if not answer_text:
        return 0.0
    ans_lower = str(answer_text).strip().lower()
    if not ans_lower:
        return 0.0
    for text in recalled_texts[:k]:
        if ans_lower in text.lower():
            return 1.0
    return 0.0


def aggregate_locomo_f1(
    recalled_texts_per_query: list[list[str]],
    answer_texts: list[str],
    k: int = 10,
) -> dict:
    """Aggregate LoCoMo F1 scores across all queries.

    Args:
        recalled_texts_per_query: For each query, the ordered list of recalled texts.
        answer_texts: The ground-truth answer for each query.
        k: Cutoff rank for evaluation.

    Returns:
        dict with "f1" (mean, std) and "num_queries".
    """
    scores = [
        locomo_f1_at_k(recalled, ans, k)
        for recalled, ans in zip(recalled_texts_per_query, answer_texts)
    ]
    arr = np.array(scores)
    return {
        "f1": {
            "mean": float(np.mean(arr)),
            "std": float(np.std(arr, ddof=1)) if len(arr) > 1 else 0.0,
        },
        "num_queries": len(scores),
    }
