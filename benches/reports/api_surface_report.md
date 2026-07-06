# MemHop API Surface Benchmark Report

**Report Name**: MemHop API Surface Benchmark
**Timestamp**: 2026-07-05T17:09:05.274170+00:00

## Test Set Description

Ground-truth retrieval evaluation uses `benches/fixtures/locomo_full.json`, the full LOCOMO benchmark (272 conversations, 1,986 labeled questions). LLM-as-judge QA evaluation samples the first 10 questions from the same full dataset to keep LLM API costs manageable. Scalability and stress scenarios use the deterministic synthetic generator in `benches/common/data_gen.rs`.

## API Coverage Overview

- Total APIs: 53
- Benchmarked APIs: 49
- Coverage Ratio: 92.5%

## API Coverage Matrix

| API | Benchmarked | Notes |
|-----|-------------|-------|
| MemHop::open | Yes | lifecycle/open |
| MemHop::sync | Yes | lifecycle/sync |
| MemHop::checkpoint | Yes | lifecycle/checkpoint |
| MemHop::close | Yes | lifecycle/close |
| MemHop::search_memory | Yes | search/search_memory param sweep |
| MemHop::process_dialogue | Yes | process_dialogue/{search_only,create_l2,import_to_l3} |
| MemHop::update_memory | Yes | update/update_memory |
| MemHop::batch_store | Yes | batch/{1,10,50} |
| MemHop::import_memory | Yes | knowledge/import_memory_l3 |
| MemHop::build_l3_hypergraph_from_path | Yes | graph/build_l3_hypergraph_from_path |
| MemHop::update_profile | Yes | profile/update_profile |
| MemHop::get_profile | Yes | profile/get_profile |
| MemHop::get_engram | Yes | engram/get_engram |
| MemHop::list_engrams | Yes | engram/list_engrams/{5,20,50} |
| MemHop::get_topic | Yes | topic/get_topic |
| MemHop::list_topics | Yes | topic/list_topics param sweep |
| MemHop::delete_topic | Yes | topic/delete_topic |
| MemHop::update_topic_title | Yes | topic/update_topic_title |
| MemHop::update_topic_title_with_refs | Yes | topic/update_topic_title_with_refs |
| MemHop::merge_topics | Yes | topic/merge_topics |
| MemHop::get_knowledge | Yes | knowledge/get_knowledge |
| MemHop::get_knowledge_nodes_by_ids | Yes | knowledge/get_knowledge_nodes_by_ids |
| MemHop::list_knowledge | Yes | knowledge/list_knowledge param sweep |
| MemHop::delete_graph | Yes | knowledge/delete_graph |
| MemHop::update_knowledge_title | Yes | knowledge/update_knowledge_title |
| MemHop::graph_query | Yes | graph/graph_query/{1,2,3} |
| MemHop::l3_query | Yes | graph/l3_query |
| MemHop::l3_detect_isolated | Yes | graph/l3_detect_isolated/{0,1,2} |
| MemHop::l3_detect_communities | Yes | graph/l3_detect_communities |
| MemHop::search_knowledge_nodes_by_keyword | Yes | graph/search_knowledge_nodes_by_keyword/{1,5,10} |
| MemHop::get_knowledge_nodes_by_type | Yes | graph/get_knowledge_nodes_by_type/{1,5,10} |
| MemHop::get_archive | Yes | archive/get_archive |
| MemHop::list_all_archives | Yes | archive/list_all_archives |
| MemHop::list_archives_by_topic | Yes | archive/list_archives_by_topic |
| MemHop::list_archives_by_nodes | Yes | archive/list_archives_by_nodes |
| MemHop::list_crystals | Yes | crystal/list_crystals |
| MemHop::delete_action_chain | Yes | crystal/delete_action_chain |
| MemHop::update_crystal_title | Yes | crystal/update_crystal_title |
| MemHop::activate_crystal | Yes | crystal/activate_crystal |
| MemHop::save_pathways | Yes | pathway/save_pathways |
| MemHop::load_pathways | Yes | pathway/load_pathways |
| MemHop::list_pathways | Yes | pathway/list_pathways |
| MemHop::activate_topic | Yes | session/activate_topic |
| MemHop::deactivate_topic | Yes | session/deactivate_topic |
| MemHop::get_active_topic_ids | Yes | session/get_active_topic_ids |
| MemHop::adjust_activation | No | no bench function; needs implementation |
| MemHop::purge_expired_sessions | No | no bench function; needs implementation |
| MemHop::session_count | Yes | session/session_count |
| MemHop::sessions_empty | Yes | session/sessions_empty |
| MemHop::extend_file | Yes | file_mgmt/extend_file |
| MemHop::allocate_page | Yes | file_mgmt/allocate_page |
| MemHop::set_encoder | No | custom-encoder setup; not benchmarked by Criterion |
| MemHop::dream | No | run with --ignored; requires LLM key |

## Per-API Latency

| API | Parameter | P50 (ms) | P95 (ms) | P99 (ms) | Throughput (QPS) |
|-----|-----------|----------|----------|----------|------------------|
| e2e_workflow/10 | 10 | 1447.567 | 4712.224 | 5166.976 | 0.5 |
| e2e_workflow/100 | 100 | 3271.067 | 3670.523 | 3827.113 | 0.3 |
| engram/get_engram | get_engram | 13.023 | 14.609 | 14.633 | 77.4 |
| engram/list_engrams | list_engrams | 20.080 | 34.638 | 35.716 | 49.7 |
| archive/list_archives_by_nodes | list_archives_by_nodes | 13.415 | 14.330 | 14.344 | 75.6 |
| archive/list_all_archives | list_all_archives | 12.755 | 14.768 | 15.111 | 77.8 |
| archive/get_archive | get_archive | 12.620 | 13.827 | 14.062 | 78.8 |
| graph/build_l3_hypergraph_from_path | build_l3_hypergraph_from_path | 11.900 | 14.133 | 14.394 | 80.8 |
| graph/graph_query | graph_query | 19.979 | 34.543 | 35.703 | 50.4 |
| graph/get_knowledge_nodes_by_type | get_knowledge_nodes_by_type | 19.484 | 32.409 | 33.496 | 52.1 |
| graph/l3_detect_communities | l3_detect_communities | 19.167 | 32.415 | 33.314 | 52.8 |
| graph/search_knowledge_nodes_by_keyword | search_knowledge_nodes_by_keyword | 19.342 | 32.198 | 33.132 | 52.5 |
| graph/l3_detect_isolated | l3_detect_isolated | 19.443 | 33.755 | 34.993 | 51.3 |
| graph/l3_query | l3_query | 19.595 | 33.327 | 34.468 | 51.9 |
| lifecycle/checkpoint | checkpoint | 26.398 | 30.245 | 30.787 | 37.2 |
| lifecycle/close | close | 21.186 | 24.040 | 24.443 | 46.9 |
| lifecycle/sync | sync | 16.917 | 18.698 | 18.979 | 58.5 |
| lifecycle/open | open | 29.552 | 32.876 | 32.999 | 33.3 |
| llm_judge_qa/qa_pipeline | qa_pipeline | 8347.632 | 10089.152 | 10186.622 | 0.1 |
| file_management/extend_file | extend_file | 32.037 | 33.580 | 33.974 | 31.2 |
| file_management/allocate_page | allocate_page | 13.123 | 14.461 | 14.472 | 78.9 |
| crystal/delete_action_chain | delete_action_chain | 13.079 | 14.821 | 15.036 | 77.2 |
| crystal/activate_crystal | activate_crystal | 13.031 | 15.197 | 15.935 | 79.6 |
| crystal/update_crystal_title | update_crystal_title | 11.923 | 13.730 | 14.088 | 84.4 |
| topic/delete_topic | delete_topic | 13.482 | 14.668 | 14.892 | 74.6 |
| topic/update_topic_title_with_refs | update_topic_title_with_refs | 14.171 | 15.029 | 15.276 | 71.7 |
| topic/update_memory | update_memory | 15.462 | 17.722 | 18.047 | 62.7 |
| topic/merge_topics | merge_topics | 13.458 | 15.825 | 16.249 | 74.3 |
| topic/get_topic | get_topic | 13.305 | 15.303 | 15.355 | 76.1 |
| topic/update_topic_title | update_topic_title | 14.000 | 17.716 | 19.116 | 69.1 |
| search/search_memory | search_memory | 38.923 | 46.437 | 47.189 | 25.3 |
| retrieval/search_memory throughput | search_memory throughput | 2459.350 | 2541.004 | 2554.324 | 0.4 |
| profile/get_profile | get_profile | 54.176 | 93.780 | 97.908 | 19.5 |
| profile/update_profile | update_profile | 24.664 | 27.477 | 28.400 | 40.6 |
| knowledge/delete_graph | delete_graph | 13.175 | 13.784 | 13.994 | 78.1 |
| knowledge/get_knowledge | get_knowledge | 13.434 | 14.202 | 14.221 | 77.3 |
| knowledge/update_knowledge_title | update_knowledge_title | 13.540 | 14.336 | 14.430 | 73.9 |
| knowledge/get_knowledge_nodes_by_ids | get_knowledge_nodes_by_ids | 13.154 | 14.640 | 14.988 | 76.6 |
| knowledge/import_memory_l3 | import_memory_l3 | 12.250 | 13.842 | 13.947 | 80.3 |
| batch/50 | 50 | 1156.317 | 1174.067 | 1179.834 | 0.9 |
| batch/1 | 1 | 35.869 | 43.702 | 45.397 | 27.2 |
| batch/10 | 10 | 251.581 | 259.652 | 262.267 | 4.0 |
| pathway/save_pathways | save_pathways | 12.487 | 13.289 | 13.314 | 80.9 |
| pathway/load_pathways | load_pathways | 19.899 | 34.571 | 35.936 | 50.0 |
| dream/dream | dream | 6994.524 | 7956.175 | 8434.197 | 0.1 |
| session/purge_expired_sessions | purge_expired_sessions | 2.844 | 6.820 | 7.307 | 289.0 |
| session/adjust_activation | adjust_activation | 3.351 | 10.032 | 10.237 | 210.5 |
| session/sessions_empty | sessions_empty | 13.283 | 15.165 | 15.218 | 74.8 |
| session/session_count | session_count | 14.245 | 15.274 | 15.293 | 70.6 |
| session/deactivate_topic | deactivate_topic | 13.961 | 18.555 | 20.663 | 68.5 |
| session/get_active_topic_ids | get_active_topic_ids | 14.260 | 20.323 | 22.773 | 66.7 |
| session/activate_topic | activate_topic | 12.901 | 14.922 | 15.210 | 76.4 |

## Retrieval Quality (locomo_full)

| Metric | Value |
|--------|-------|
| Recall@1 | 0.314 |
| Recall@5 | 0.661 |
| Recall@10 | 0.664 |
| MRR | 0.486 |
| NDCG@10 | 0.515 |
| Precision@5 | 0.151 |
| Latency P50 | 51.1ms |
| Latency P95 | 63.9ms |
| Latency P99 | 81.1ms |

## LLM-as-Judge QA Scores

No LLM-as-Judge QA evaluation was run (missing `MEMHOP_LLM_API_KEY` or skipped).

## Competitor Comparison

| Competitor | Test Set | Metric | Score |
|------------|----------|--------|-------|
| Mem0 Platform | locomo | accuracy | 92.500 |
| Mem0 Platform | longmemeval | accuracy | 94.400 |
| Mem0 OSS | locomo | accuracy | 67.130 |
| Mem0 OSS | longmemeval | accuracy | 49.000 |
| Letta (MemGPT) | locomo | accuracy | 83.200 |
| Zep / Graphiti | longmemeval | accuracy | 63.800 |
| Cognee | locomo | accuracy | N/A |
| LangMem | locomo | accuracy | N/A |
| agentmemory | longmemeval_s | recall@5 | 95.200 |

## Conclusion

The API surface benchmark covers 49 of 53 public APIs (92.5%). Retrieval quality is measured against the active fixture (`locomo_full`) using hybrid BM25 + vector + entity search. Latency percentiles are computed from repeated API calls with the mock gRPC encoder on a local machine.

## Limitations

- LOCOMO retrieval metrics are recall-based and may not be directly comparable to LLM-judge accuracy scores reported by competitors.
- LLM-as-Judge QA samples the first 10 questions from the full LOCOMO dataset to keep API costs manageable and is only run when `--ignored` is passed.
- Competitor scores mix different metrics (accuracy vs. recall@K) and may not be directly comparable.
- Latency includes the mock `meowvec` gRPC vector-encoding overhead; production encoder latency will differ.
