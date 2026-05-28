"""Dataset adapters for benchmark integration."""

from .schema import RetrievalDataset, RetrievalResult, LatencyResult
from .c_mteb_adapter import load_c_mteb_task, load_all_c_mteb_retrieval, C_MTEB_RETRIEVAL_TASKS
from .beir_adapter import load_beir_dataset, load_all_beir, BEIR_DATASETS

__all__ = [
    "RetrievalDataset", "RetrievalResult", "LatencyResult",
    "load_c_mteb_task", "load_all_c_mteb_retrieval", "C_MTEB_RETRIEVAL_TASKS",
    "load_beir_dataset", "load_all_beir", "BEIR_DATASETS",
]
