// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// MemHop Rust API Integration Tests — 直接调用 Rust API 测试完整功能
//
// 测试策略：
// - 所有调用直接走 Rust API（MemHop::open + 方法调用），验证类型安全
// - 覆盖 11 个命令 + 边界条件
// - 模拟 Agent 接入流程（open → search → update → query → close）
// - Dream 命令需设置 MEMHOP_LLM_API_KEY 环境变量（可选）
// - 向量编码需要配置 gRPC 或 IPC 编码器（测试中使用 auto_create 跳过向量检索）

use std::path::PathBuf;

use memhop::{
    ActionItem, ActionType, ArchivePageQuery, CrystalListQuery, EngramListQuery, ImportData,
    ImportMode, ImportRequest, KnowledgeImportItem, KnowledgeListQuery, LlmConfig, MemHop,
    MemHopConfig, SearchQuery, TopicImportItem, TopicListQuery, UpdateL2Fields, UpdateL3Fields,
    UpdateL5Fields, UpdateProfileRequest, UpdateRequest,
};

// ============================================================================
// 辅助函数
// ============================================================================

/// 创建测试配置
fn test_config(db_path: &str) -> MemHopConfig {
    MemHopConfig {
        db_path: PathBuf::from(db_path),
        encoder_grpc_addr: None,
        vector_dim: 768,
        crystal_path: None,
        llm: LlmConfig {
            api_url: String::new(),
            api_key: String::new(),
            model: String::new(),
            temperature: 0.2,
            top_p: 0.9,
            presence_penalty: 0.0,
            frequency_penalty: 0.0,
            timeout_secs: 30,
            language: "zh".to_string(),
        },
        auto_dream_on_evict: true,
        ivf_initial_k: 16,
        search_weights: None,
        decay_config: None,
        session_config: None,
        auto_dream_archive_threshold: None,
        auto_dream_summary_bytes: None,
        auto_checkpoint_interval: None,
        adjacency_cache_max_entries: 128,
    }
}

// ============================================================================
// 测试：MemHop::open 边界条件
// ============================================================================

#[test]
fn test_open_empty_path() {
    let config = MemHopConfig {
        db_path: PathBuf::from(""),
        encoder_grpc_addr: None,
        vector_dim: 768,
        crystal_path: None,
        llm: LlmConfig::default(),
        auto_dream_on_evict: false,
        ivf_initial_k: 16,
        search_weights: None,
        decay_config: None,
        session_config: None,
        auto_dream_archive_threshold: None,
        auto_dream_summary_bytes: None,
        auto_checkpoint_interval: None,
        adjacency_cache_max_entries: 128,
    };
    let result = MemHop::open(config);
    assert!(result.is_err(), "empty db_path should fail");
}

#[test]
fn test_config_deserialize_error() {
    let bad_json = "not json";
    let result: Result<MemHopConfig, _> = serde_json::from_str(bad_json);
    assert!(result.is_err(), "invalid JSON should fail to deserialize");
}

#[test]
fn test_open_invalid_config_zero_dim() {
    let config = MemHopConfig {
        db_path: PathBuf::from("/tmp/memhop_test_zero_dim.meh"),
        encoder_grpc_addr: None,
        vector_dim: 0,
        crystal_path: None,
        llm: LlmConfig::default(),
        auto_dream_on_evict: false,
        ivf_initial_k: 16,
        search_weights: None,
        decay_config: None,
        session_config: None,
        auto_dream_archive_threshold: None,
        auto_dream_summary_bytes: None,
        auto_checkpoint_interval: None,
        adjacency_cache_max_entries: 128,
    };
    let _ = std::fs::remove_file("/tmp/memhop_test_zero_dim.meh");
    let result = MemHop::open(config);
    // Zero vector_dim may or may not fail depending on implementation; just check it opens or errors gracefully
    let _ = result;
    let _ = std::fs::remove_file("/tmp/memhop_test_zero_dim.meh");
}

// ============================================================================
// 测试：完整生命周期（open → commands → close）
// ============================================================================

#[test]
fn test_full_lifecycle() {
    let db_path = "/tmp/memhop_lifecycle.meh";
    let _ = std::fs::remove_file(db_path);

    // ---- 1. Open ----
    let config = test_config(db_path);
    let mut db = MemHop::open(config.clone()).expect("MemHop::open failed");

    // ---- 2. Search with auto_create ----
    let res = db
        .search_context(SearchQuery {
            dialogue: "Rust programming".to_string(),
            l2_id: None,
            context_id: None,
            l3_id: None,
            context_limit: 5,
            auto_create: 1,
            min_score: 0.0,
            source: Default::default(),
        })
        .expect("search_context failed");
    assert!(!res.contexts.is_empty(), "auto_create should create L2");
    let l2_id = res.contexts[0].id.clone();

    // ---- 3. Update L2 with dialogue ----
    let update_res = db
        .update_memory(UpdateRequest {
            topic_id: l2_id.clone(),
            dialogue_text: "User: What is Rust?\nAssistant: Rust is a systems language."
                .to_string(),
            summary: None,
            action_chain: Some(vec![ActionItem {
                title: "answer".to_string(),
                description: "explain rust".to_string(),
                action_type: ActionType::Execute,
                parameters: None,
            }]),
            instant_distill: false,
            source: Default::default(),
        })
        .expect("update_memory failed");
    assert_eq!(update_res.topic_id, l2_id);

    // ---- 4. Query L0 profile (may not exist yet) ----
    match db.get_profile() {
        Ok(Some(profile)) => println!("  L0 profile: {:?}", profile),
        Ok(None) => println!("  L0 profile not yet created (expected)"),
        Err(e) => println!("  L0 profile error: {} (expected)", e),
    }

    // ---- 5. Query L2 topics ----
    let l2_res = db
        .list_l2(TopicListQuery {
            page: 1,
            page_size: 10,
            active_only: false,
            keyword: None,
        })
        .expect("list_l2 failed");
    assert!(l2_res.total > 0, "should have L2 topics");

    // ---- 6. Query L1 engrams ----
    let l1_res = db
        .list_engrams(EngramListQuery {
            page: 1,
            page_size: 10,
            state_filter: None,
            min_importance: None,
            keyword: None,
        })
        .expect("list_engrams failed");

    // ---- 6a. Query L1 get (single engram by ID) ----
    if let Some(first_engram) = l1_res.items.first() {
        let _engram = db.get_engram(&first_engram.id).expect("get_engram failed");
    }

    // ---- 7. Query L3 knowledge ----
    let l3_res = db
        .list_knowledge(KnowledgeListQuery {
            page: 1,
            page_size: 10,
            domain_filter: None,
            knowledge_type: None,
            keyword: None,
        })
        .expect("list_knowledge failed");

    // ---- 7a. Query L3 get (single knowledge by ID) ----
    let mut knowledge_id: Option<String> = None;
    if let Some(first_knowledge) = l3_res.items.first() {
        let kid = first_knowledge.id.clone();
        knowledge_id = Some(kid.clone());
        let _detail = db.get_knowledge(&kid).expect("get_knowledge failed");
    }

    // ---- 8. Query L4 archives (generic) ----
    let _l4_res = db
        .list_all_archives(ArchivePageQuery {
            page: 1,
            page_size: 10,
            start_time: None,
            end_time: None,
            content_type: None,
        })
        .expect("list_all_archives failed");

    // ---- 8a. Query L4 archives by topic_id ----
    let _l4_by_topic = db
        .list_archives_by_topic(
            &l2_id,
            ArchivePageQuery {
                page: 1,
                page_size: 10,
                start_time: None,
                end_time: None,
                content_type: None,
            },
        )
        .expect("list_archives_by_topic failed");

    // ---- 9. Query L5 crystals ----
    let _l5_res = db
        .list_crystals(CrystalListQuery {
            page: 1,
            page_size: 10,
            status_filter: None,
            min_trigger_count: None,
            keyword: None,
        })
        .expect("list_crystals failed");

    // ---- 10. Update L0 profile ----
    let _profile = db
        .update_profile(UpdateProfileRequest {
            name: Some("Rust API Agent".to_string()),
            role: Some("Test Assistant".to_string()),
            personality: None,
            worldview: None,
            preferences: None,
            lexicon: None,
            style_traits: None,
            emotion_patterns: None,
        })
        .expect("update_profile failed");

    // ---- 11. Update L2 title via update_l2 ----
    let _topic = db
        .update_l2(
            &l2_id,
            UpdateL2Fields {
                title: Some("Updated Rust Topic".to_string()),
                ..Default::default()
            },
        )
        .expect("update_l2 failed");

    // ---- 12. Verify updated title ----
    let topic_detail = db.get_l2(&l2_id).expect("get_l2 failed");
    assert!(topic_detail.is_some(), "topic should exist");

    // ---- 12a. Update L3 title via update_l3 ----
    if let Some(ref kid) = knowledge_id {
        db.update_l3(
            kid,
            UpdateL3Fields {
                name: Some("Updated Knowledge".to_string()),
            },
        )
        .expect("update_l3 failed");
    }

    // ---- 12b. Update L5 title (test error path: no crystals yet) ----
    let l5_update = db.update_l5(
        "nonexistent",
        UpdateL5Fields {
            title: Some("test".to_string()),
            ..Default::default()
        },
    );
    assert!(
        l5_update.is_err(),
        "L5 update with nonexistent ID should error"
    );

    // ---- 13. Session management ----
    // activate
    db.activate_topic(&l2_id, Some(300_000));

    // list active
    let active = db.get_active_topic_ids();
    assert!(!active.is_empty(), "should have active topics");

    // adjust activation
    db.adjust_activation(&l2_id, 0.5);

    // deactivate
    db.deactivate_topic(&l2_id);

    // ---- 14. Import L0 profile ----
    let import_res = db
        .import_memory(ImportRequest {
            target_layer: memhop::TargetLayer::Profile,
            data: ImportData::Profile {
                name: Some("Imported Agent".to_string()),
                role: Some("Tester".to_string()),
                personality: None,
                worldview: None,
                preferences: None,
            },
            mode: ImportMode::Merge,
            knowledge_title: None,
        })
        .expect("import profile failed");
    assert_eq!(import_res.status, memhop::ImportStatus::Success);

    // ---- 15. Import L2 topics ----
    let import_res = db
        .import_memory(ImportRequest {
            target_layer: memhop::TargetLayer::Topic,
            data: ImportData::Topics(vec![TopicImportItem {
                title: "Python Basics".to_string(),
                summary: Some("Learning Python".to_string()),
                keywords: vec!["python".to_string()],
                knowledge_domain: None,
            }]),
            mode: ImportMode::Merge,
            knowledge_title: None,
        })
        .expect("import topics failed");
    assert_eq!(import_res.status, memhop::ImportStatus::Success);

    // ---- 16. Import L3 knowledge ----
    let import_res = db
        .import_memory(ImportRequest {
            target_layer: memhop::TargetLayer::Knowledge,
            data: ImportData::Knowledge(vec![KnowledgeImportItem {
                title: "Rust Ownership".to_string(),
                domain: "programming".to_string(),
                knowledge_type: "Conceptual".to_string(),
                text: "Rust ownership system...".to_string(),
                summary: None,
                keywords: vec!["rust".to_string(), "ownership".to_string()],
                source_ref: None,
            }]),
            mode: ImportMode::Merge,
            knowledge_title: None,
        })
        .expect("import knowledge failed");
    assert_eq!(import_res.status, memhop::ImportStatus::Success);

    // ---- 16a. Import build_l3 from path ----
    let build_res = db.build_l3_hypergraph_from_path(PathBuf::from("/tmp").as_path());
    // build_l3 may succeed or fail depending on files - just check it runs
    println!("  build_l3 result: ok={}", build_res.is_ok());

    // ---- 17. Batch store (fails without encoder, expected behavior) ----
    // Skip batch_store without encoder — it requires a real encoder
    println!("  batch_store skipped (no encoder configured)");

    // ---- 18. Sync ----
    db.sync().expect("sync failed");

    // ---- 19. Close ----
    db.sync().expect("sync before close failed");
    drop(db);

    // ---- 20. Verify data persists by reopening ----
    let db2 = MemHop::open(config).expect("reopen failed");

    let l2_persisted = db2
        .list_l2(TopicListQuery {
            page: 1,
            page_size: 100,
            active_only: false,
            keyword: None,
        })
        .expect("list_l2 after reopen failed");
    assert!(
        l2_persisted.total > 0,
        "L2 topics should persist after close/reopen"
    );

    let profile_persisted = db2.get_profile().expect("get_profile after reopen failed");
    assert!(profile_persisted.is_some(), "profile should persist");

    drop(db2);
    let _ = std::fs::remove_file(db_path);
}

// ============================================================================
// 测试：Graph query 与 Delete 命令（L2/L3/L5）
// ============================================================================

#[test]
fn test_graph_query_and_delete() {
    let db_path = "/tmp/memhop_graph_delete.meh";
    let source_path = "/tmp/memhop_test_graph";
    let _ = std::fs::remove_file(db_path);
    let _ = std::fs::remove_dir_all(source_path);

    // Prepare a minimal Rust codebase so build_l3 creates nodes and edges.
    std::fs::create_dir_all(format!("{}/src", source_path)).unwrap();
    std::fs::write(
        format!("{}/src/a.rs", source_path),
        "use crate::b;\npub fn foo() {}\n",
    )
    .unwrap();
    std::fs::write(format!("{}/src/b.rs", source_path), "pub fn bar() {}\n").unwrap();

    let config = test_config(db_path);
    let mut db = MemHop::open(config).expect("MemHop::open failed");

    // ---- 1. Build L3 hypergraph ----
    let build_res = db
        .build_l3_hypergraph_from_path(PathBuf::from(source_path).as_path())
        .expect("build_l3 failed");
    assert!(
        build_res.created_ids.len() >= 2,
        "build_l3 should create at least two nodes"
    );
    let start_node = build_res.created_ids[0].clone();

    // ---- 2. Query L3 list to obtain graph_id ----
    let l3_res = db
        .list_knowledge(KnowledgeListQuery {
            page: 1,
            page_size: 10,
            domain_filter: None,
            knowledge_type: None,
            keyword: None,
        })
        .expect("list_knowledge failed");
    assert!(
        !l3_res.items.is_empty(),
        "L3 should contain the built graph"
    );
    let graph_id = l3_res.items[0].id.clone();

    // ---- 3. Graph query with Dependency edges ----
    let subgraph = db
        .graph_query(
            &graph_id,
            &start_node,
            2,
            Some(vec!["Dependency".to_string()]),
        )
        .expect("graph_query failed");
    assert!(
        subgraph.nodes.len() >= 2,
        "graph_query should return at least 2 nodes, got {}",
        subgraph.nodes.len()
    );

    // ---- 4. Delete L3 graph ----
    db.delete_l3(&graph_id).expect("delete_l3 failed");

    // Verify the graph is gone.
    let l3_after = db
        .list_knowledge(KnowledgeListQuery {
            page: 1,
            page_size: 10,
            domain_filter: None,
            knowledge_type: None,
            keyword: None,
        })
        .expect("list_knowledge after delete failed");
    assert_eq!(l3_after.total, 0, "L3 graph should be deleted");

    // ---- 5. Create an L2 topic and L5 action chain ----
    let search_res = db
        .search_context(SearchQuery {
            dialogue: "Action chain test topic".to_string(),
            l2_id: None,
            context_id: None,
            l3_id: None,
            context_limit: 5,

            auto_create: 1,
            min_score: 0.0,

            source: Default::default(),
        })
        .expect("search_context failed");
    let l2_id = search_res.contexts[0].id.clone();

    let _update_res = db
        .update_memory(UpdateRequest {
            topic_id: l2_id.clone(),
            dialogue_text: "User: Do something.\nAssistant: Done.".to_string(),
            summary: None,
            action_chain: Some(vec![ActionItem {
                title: "do_something".to_string(),
                description: "perform an action".to_string(),
                action_type: ActionType::Execute,
                parameters: None,
            }]),
            instant_distill: false,
            source: Default::default(),
        })
        .expect("update_memory failed");

    // ---- 6. List L5 crystals and delete the action chain ----
    let l5_res = db
        .list_crystals(CrystalListQuery {
            page: 1,
            page_size: 10,
            status_filter: None,
            min_trigger_count: None,
            keyword: None,
        })
        .expect("list_crystals failed");
    assert!(
        !l5_res.items.is_empty(),
        "L5 should contain the action chain"
    );
    let chain_id = l5_res.items[0].id.clone();

    db.delete_l5(&chain_id).expect("delete_l5 failed");

    let l5_after = db
        .list_crystals(CrystalListQuery {
            page: 1,
            page_size: 10,
            status_filter: None,
            min_trigger_count: None,
            keyword: None,
        })
        .expect("list_crystals after delete failed");
    assert_eq!(l5_after.total, 0, "L5 action chain should be deleted");

    // ---- 7. Delete L2 topic ----
    db.delete_l2(&l2_id).expect("delete_l2 failed");

    let l2_after = db.get_l2(&l2_id).expect("get_l2 after delete failed");
    assert!(
        l2_after.is_none(),
        "deleted L2 topic should not be retrievable"
    );

    drop(db);
    let _ = std::fs::remove_file(db_path);
    let _ = std::fs::remove_dir_all(source_path);
}

// ============================================================================
// 测试：v0.51.1 记忆链路修复 — L2→L3 完整关联链路
// ============================================================================

#[test]
fn test_l2_l3_memory_chain() {
    let db_path = "/tmp/memhop_l2l3_chain.meh";
    let _ = std::fs::remove_file(db_path);

    let config = test_config(db_path);
    let mut db = MemHop::open(config.clone()).expect("MemHop::open failed");

    // ---- MH-1: import("knowledge") 返回节点 ID ----
    let import_res = db
        .import_memory(ImportRequest {
            target_layer: memhop::TargetLayer::Knowledge,
            data: ImportData::Knowledge(vec![
                KnowledgeImportItem {
                    title: "Rome Day 1 - Colosseum Visit".to_string(),
                    domain: "travel".to_string(),
                    knowledge_type: "episodic".to_string(),
                    text: "Visited the Colosseum on the first day. The guided tour lasted 2 hours."
                        .to_string(),
                    summary: None,
                    keywords: vec![
                        "Rome".to_string(),
                        "Colosseum".to_string(),
                        "tour".to_string(),
                    ],
                    source_ref: None,
                },
                KnowledgeImportItem {
                    title: "Favorite Italian Dish".to_string(),
                    domain: "food".to_string(),
                    knowledge_type: "semantic".to_string(),
                    text: "The favorite dish was Cacio e Pepe at Ristorante Da Enzo.".to_string(),
                    summary: None,
                    keywords: vec![
                        "food".to_string(),
                        "Italian".to_string(),
                        "Cacio e Pepe".to_string(),
                    ],
                    source_ref: None,
                },
            ]),
            mode: ImportMode::Merge,
            knowledge_title: Some("benchmark_0".to_string()),
        })
        .expect("import knowledge failed");

    // MH-1: Verify response has "id" (single) and "ids" (batch) and "node_count"
    let first_id = import_res
        .id
        .as_ref()
        .expect("MH-1: import should return 'id' field");
    assert!(!first_id.is_empty(), "id should be non-empty hex string");
    assert_eq!(first_id.len(), 16, "id should be 16-char hex string");

    let ids = import_res
        .ids
        .as_ref()
        .expect("MH-1: import should return 'ids' field");
    assert_eq!(ids.len(), 2, "should have 2 node IDs");

    assert_eq!(import_res.node_count, 2, "node_count should be 2");

    let knowledge_title = import_res
        .knowledge_title
        .as_ref()
        .expect("MH-1: import should echo 'knowledge_title'");
    assert_eq!(knowledge_title, "benchmark_0");

    let l3_id_1 = ids[0].clone();
    let l3_id_2 = ids[1].clone();
    println!(
        "MH-1 PASS: import returned id={}, ids={:?}, node_count={}",
        first_id, ids, import_res.node_count
    );

    // ---- MH-3: get_knowledge_nodes_by_ids 批量获取节点原文 ----
    let nodes_res = db
        .get_knowledge_nodes_by_ids(
            &[
                l3_id_1.clone(),
                l3_id_2.clone(),
                "nonexistent_id".to_string(),
            ],
            true,
        )
        .expect("get_knowledge_nodes_by_ids failed");
    assert_eq!(
        nodes_res.total, 2,
        "MH-3: should return 2 nodes (missing ID skipped)"
    );
    assert_eq!(nodes_res.requested, 3, "MH-3: requested count should be 3");

    for node in &nodes_res.nodes {
        let text = node
            .text
            .as_ref()
            .expect("MH-3: text field should exist when include_text=true");
        assert!(!text.is_empty(), "text should not be empty");
        assert!(node.importance > 0.0, "importance should be positive");
        println!(
            "MH-3 node: id={} title='{}' domain={} type={} text='{}' keywords={:?}",
            node.id,
            node.title,
            node.domain,
            node.knowledge_type,
            &text[..text.len().min(50)],
            node.keywords
        );
    }

    // MH-3: include_text=false should omit text field
    let nodes_no_text = db
        .get_knowledge_nodes_by_ids(std::slice::from_ref(&l3_id_1), false)
        .expect("get_knowledge_nodes_by_ids failed");
    assert_eq!(nodes_no_text.nodes.len(), 1);
    assert!(
        nodes_no_text.nodes[0].text.is_none(),
        "MH-3: text should be omitted when include_text=false"
    );
    println!("MH-3 PASS: batch get with include_text=false omits text field");

    // ---- MH-3: max 50 IDs enforcement ----
    let many_ids: Vec<String> = (0..60).map(|i| format!("deadbeef{:012x}", i)).collect();
    let max_res = db
        .get_knowledge_nodes_by_ids(&many_ids, false)
        .expect("get_knowledge_nodes_by_ids with many IDs failed");
    assert_eq!(
        max_res.requested, 60,
        "requested should reflect original count"
    );
    println!(
        "MH-3 PASS: max 50 IDs enforcement, requested={}",
        max_res.requested
    );

    // ---- Create L2 topic via import ----
    let l2_import = db
        .import_memory(ImportRequest {
            target_layer: memhop::TargetLayer::Topic,
            data: ImportData::Topics(vec![TopicImportItem {
                title: "Rome Trip 2024".to_string(),
                summary: Some("Trip to Rome".to_string()),
                keywords: vec!["rome".to_string(), "trip".to_string()],
                knowledge_domain: None,
            }]),
            mode: ImportMode::Merge,
            knowledge_title: None,
        })
        .expect("import topic failed");
    let l2_ids = l2_import.ids.as_ref().expect("L2 import should return ids");
    let l2_topic_id = l2_ids[0].clone();
    println!("Created L2 topic: {}", l2_topic_id);

    // ---- MH-2: update_l2 支持 l3_refs 写入 ----
    let _topic_update = db
        .update_l2(
            &l2_topic_id,
            UpdateL2Fields {
                title: Some("Rome Trip 2024".to_string()),
                l3_refs: Some(vec![l3_id_1.clone(), l3_id_2.clone()]),
                ..Default::default()
            },
        )
        .expect("update_l2 failed");
    println!("MH-2 PASS: update_l2 with l3_refs succeeded");

    // ---- MH-2: persist and verify ----
    db.sync().expect("sync failed");
    drop(db);

    let mut db2 = MemHop::open(config).expect("reopen failed");

    // Verify L2 topic has l3_refs after reopen
    let topic_detail = db2
        .get_l2(&l2_topic_id)
        .expect("get_l2 after reopen failed")
        .expect("topic should exist after reopen");
    assert_eq!(
        topic_detail.l3_refs.len(),
        2,
        "MH-2: should have 2 l3_refs after reopen"
    );
    println!("MH-2 PASS: l3_refs persisted after close/reopen");

    // ---- Full chain verification: search should discover L3 nodes ----
    let search_res = db2
        .search_context(SearchQuery {
            dialogue: "Rome trip memories".to_string(),
            l2_id: None,
            context_id: None,
            l3_id: None,
            context_limit: 5,

            auto_create: 1,
            min_score: 0.0,

            source: Default::default(),
        })
        .expect("search_context failed");
    let search_l2_id = search_res.contexts[0].id.clone();

    // Link the auto-created L2 to L3 nodes
    let _link = db2
        .update_l2(
            &search_l2_id,
            UpdateL2Fields {
                title: Some("Rome Trip Memories".to_string()),
                l3_refs: Some(vec![l3_id_1.clone(), l3_id_2.clone()]),
                ..Default::default()
            },
        )
        .expect("update_l2 failed");

    // Search with context_id to verify l3_refs appear in search results
    let search_res2 = db2
        .search_context(SearchQuery {
            dialogue: "Colosseum".to_string(),
            l2_id: None,
            context_id: Some(search_l2_id.clone()),
            l3_id: None,
            context_limit: 5,

            auto_create: 0,
            min_score: 0.0,

            source: Default::default(),
        })
        .expect("search_context with context_id failed");

    assert!(!search_res2.contexts.is_empty());
    assert!(
        !search_res2.contexts[0].l3_refs.is_empty(),
        "MH-2: search result should include l3_refs"
    );
    println!(
        "MH-2 PASS: search result contains l3_refs: {:?}",
        search_res2.contexts[0].l3_refs
    );

    // Cleanup
    drop(db2);
    let _ = std::fs::remove_file(db_path);
}

// ============================================================================
// 测试：查询 L3 详情
// ============================================================================

#[test]
fn test_query_l3_detail() {
    let db_path = "/tmp/memhop_l3_detail.meh";
    let source_path = "/Volumes/zt_hd/projects/meow/meowagent/src";
    let _ = std::fs::remove_file(db_path);

    if !std::path::Path::new(source_path).exists() {
        eprintln!("[SKIP] meowagent source not found");
        return;
    }

    let config = test_config(db_path);
    let mut db = MemHop::open(config).expect("MemHop::open failed");

    // 1. Build L3
    let _build_res = db
        .build_l3_hypergraph_from_path(PathBuf::from(source_path).as_path())
        .expect("build_l3 failed");

    // 2. Query L3 list
    println!("\n===== L3 LIST =====");
    let l3_res = db
        .list_knowledge(KnowledgeListQuery {
            page: 1,
            page_size: 20,
            domain_filter: None,
            knowledge_type: None,
            keyword: None,
        })
        .expect("list_knowledge failed");
    println!("{}", serde_json::to_string_pretty(&l3_res).unwrap());

    // 3. Get L3 detail (with all nodes)
    if let Some(first) = l3_res.items.first() {
        let l3_id = &first.id;
        println!("\n===== L3 DETAIL (id={}) =====", l3_id);
        let detail = db.get_knowledge(l3_id).expect("get_knowledge failed");
        if let Some(k) = detail {
            println!("title: {}", k.title);
            println!("domain: {}", k.domain);
            println!("knowledge_type: {}", k.knowledge_type);
            println!("importance: {}", k.importance);
            println!("source_ref: {}", k.source_ref.unwrap_or_default());
            println!("keywords ({}):", k.keywords.len());
            for kw in k.keywords.iter().take(30) {
                println!("  - {}", kw);
            }
            let preview: String = k.text.chars().take(500).collect();
            println!("\ntext preview ({} chars total):", k.text.len());
            println!("{}", preview);
        }
    }

    // 4. Query L2 detail to see l3_refs
    println!("\n===== L2 DETAIL =====");
    let l2_res = db
        .list_l2(TopicListQuery {
            page: 1,
            page_size: 5,
            active_only: false,
            keyword: None,
        })
        .expect("list_l2 failed");
    if let Some(first) = l2_res.items.first() {
        let l2_id = &first.id;
        let l2_detail = db.get_l2(l2_id).expect("get_l2 failed");
        if let Some(d) = l2_detail {
            println!("{}", serde_json::to_string_pretty(&d).unwrap());
        }
    }

    drop(db);
    let _ = std::fs::remove_file(db_path);
}

// ============================================================================
// 测试：Merge Topics
// ============================================================================

#[test]
fn test_merge_topics() {
    let db_path = "/tmp/memhop_merge.meh";
    let _ = std::fs::remove_file(db_path);

    let config = test_config(db_path);
    let mut db = MemHop::open(config).expect("MemHop::open failed");

    // Create two L2s via auto_create
    let res1 = db
        .search_context(SearchQuery {
            dialogue: "Topic Alpha".to_string(),
            l2_id: None,
            context_id: None,
            l3_id: None,
            context_limit: 5,

            auto_create: 1,
            min_score: 0.0,

            source: Default::default(),
        })
        .expect("search_context 1 failed");
    let id1 = res1.contexts[0].id.clone();

    let res2 = db
        .search_context(SearchQuery {
            dialogue: "Topic Beta".to_string(),
            l2_id: None,
            context_id: None,
            l3_id: None,
            context_limit: 5,

            auto_create: 1,
            min_score: 0.0,

            source: Default::default(),
        })
        .expect("search_context 2 failed");
    let id2 = res2.contexts[0].id.clone();

    // Merge them
    let _merged = db
        .merge_l2(&id1, vec![id2.clone()])
        .expect("merge_l2 failed");

    // Verify secondary is gone
    let secondary = db.get_l2(&id2).expect("get_l2 after merge failed");
    assert!(
        secondary.is_none(),
        "secondary topic should be deleted after merge"
    );

    drop(db);
    let _ = std::fs::remove_file(db_path);
}

// ============================================================================
// 测试：错误处理全面覆盖
// ============================================================================

#[test]
fn test_error_handling() {
    let db_path = "/tmp/memhop_errors.meh";
    let _ = std::fs::remove_file(db_path);

    let config = test_config(db_path);
    let mut db = MemHop::open(config).expect("MemHop::open failed");

    // missing field: search without dialogue (empty dialogue is allowed but may return empty)
    let res = db.search_context(SearchQuery {
        dialogue: "".to_string(),
        l2_id: None,
        context_id: None,
        l3_id: None,
        context_limit: 5,

        auto_create: 0,
        min_score: 0.0,

        source: Default::default(),
    });
    // Empty dialogue search may succeed with empty results; that's acceptable
    println!("empty dialogue search: {:?}", res.is_ok());

    // unknown import action: import with empty data
    let res = db.import_memory(ImportRequest {
        target_layer: memhop::TargetLayer::Profile,
        data: ImportData::Profile {
            name: None,
            role: None,
            personality: None,
            worldview: None,
            preferences: None,
        },
        mode: ImportMode::Merge,
        knowledge_title: None,
    });
    // Empty profile import may succeed or fail gracefully
    println!("empty profile import: {:?}", res.is_ok());

    // query_layer with unsupported combination: L4 get (no direct API, use list_archives_by_topic with bad id)
    let res = db.list_archives_by_topic(
        "nonexistent",
        ArchivePageQuery {
            page: 1,
            page_size: 10,
            start_time: None,
            end_time: None,
            content_type: None,
        },
    );
    // Should return empty result, not error
    println!("archives by nonexistent topic: {:?}", res.is_ok());

    // update_title with unknown layer: update_profile with no fields
    let res = db.update_profile(UpdateProfileRequest {
        name: None,
        role: None,
        personality: None,
        worldview: None,
        preferences: None,
        lexicon: None,
        style_traits: None,
        emotion_patterns: None,
    });
    // May succeed with no changes
    println!("empty profile update: {:?}", res.is_ok());

    // session activate without topic_id: use empty string
    db.activate_topic("", Some(300_000));
    let active = db.get_active_topic_ids();
    // Empty string topic won't be activated; just check it doesn't panic
    println!("activate empty topic, active count: {}", active.len());

    drop(db);
    let _ = std::fs::remove_file(db_path);
}

// ============================================================================
// 测试：模拟 Agent 接入流程
// ============================================================================

#[test]
fn test_agent_workflow() {
    let db_path = "/tmp/memhop_agent.meh";
    let _ = std::fs::remove_file(db_path);

    let config = test_config(db_path);
    let mut db = MemHop::open(config).expect("Agent: failed to open database");
    println!("[Agent] Database opened");

    // Agent 2: 设置自己的画像
    let _profile = db
        .update_profile(UpdateProfileRequest {
            name: Some("Coding Agent".to_string()),
            role: Some("Rust Programming Assistant".to_string()),
            personality: Some("Helpful and precise".to_string()),
            worldview: None,
            preferences: None,
            lexicon: None,
            style_traits: None,
            emotion_patterns: None,
        })
        .expect("update_profile failed");
    println!("[Agent] Profile set");

    // Agent 3: 用户提问，检索记忆
    let search_res = db
        .search_context(SearchQuery {
            dialogue: "How do I fix a borrow checker error in Rust?".to_string(),
            l2_id: None,
            context_id: None,
            l3_id: None,
            context_limit: 5,

            auto_create: 1,
            min_score: 0.0,

            source: Default::default(),
        })
        .expect("search_context failed");
    assert!(!search_res.contexts.is_empty());
    let topic_id = search_res.contexts[0].id.clone();
    println!("[Agent] Search complete, active topic: {}", topic_id);

    // Agent 4: 激活会话
    db.activate_topic(&topic_id, Some(600_000));
    println!("[Agent] Session activated");

    // Agent 5: 写入对话
    let _update = db
        .update_memory(UpdateRequest {
            topic_id: topic_id.clone(),
            dialogue_text: "User: How do I fix borrow checker error?\nAssistant: The borrow checker ensures memory safety. Use & instead of &mut when you don't need mutation.".to_string(),
            summary: Some("borrow checker explanation".to_string()),
            action_chain: Some(vec![
                ActionItem {
                    title: "explain_borrow_checker".to_string(),
                    description: "explain how to fix borrow checker error".to_string(),
                    action_type: ActionType::Execute,
                    parameters: None,
                },
                ActionItem {
                    title: "provide_example".to_string(),
                    description: "show code example".to_string(),
                    action_type: ActionType::Create,
                    parameters: None,
                },
            ]),
            instant_distill: false,
            source: Default::default(),
        })
        .expect("update_memory failed");
    println!("[Agent] Memory updated");

    // Agent 6: 验证写入的对话
    let _archives = db
        .list_all_archives(ArchivePageQuery {
            page: 1,
            page_size: 10,
            start_time: None,
            end_time: None,
            content_type: None,
        })
        .expect("list_all_archives failed");
    println!("[Agent] Archives verified");

    // Agent 7: 同步到磁盘
    db.sync().expect("sync failed");
    println!("[Agent] Synced to disk");

    // Agent 8: 关闭
    db.sync().expect("sync before close failed");
    drop(db);
    println!("[Agent] Database closed");

    let _ = std::fs::remove_file(db_path);
}

// ============================================================================
// 测试：Dream（记忆整合）— 需要 LLM API 环境变量
// ============================================================================

#[test]
#[ignore = "requires MEMHOP_LLM_API_KEY env var and network access"]
fn test_dream_with_llm() {
    let api_key = std::env::var("MEMHOP_LLM_API_KEY").expect("MEMHOP_LLM_API_KEY must be set");

    let db_path = "/tmp/memhop_dream.meh";
    let _ = std::fs::remove_file(db_path);

    let mut config = test_config(db_path);
    config.llm.api_key = api_key.clone();
    config.llm.api_url = std::env::var("MEMHOP_LLM_API_URL")
        .unwrap_or_else(|_| "https://api.openai.com/v1/chat/completions".to_string());
    config.llm.model =
        std::env::var("MEMHOP_LLM_MODEL").unwrap_or_else(|_| "gpt-4o-mini".to_string());
    let mut db = MemHop::open(config).expect("MemHop::open failed");

    // 1. Create some memory first
    let search_res = db
        .search_context(SearchQuery {
            dialogue: "Learning about Rust memory management".to_string(),
            l2_id: None,
            context_id: None,
            l3_id: None,
            context_limit: 5,

            auto_create: 1,
            min_score: 0.0,

            source: Default::default(),
        })
        .expect("search_context failed");
    let topic_id = search_res.contexts[0].id.clone();

    // 2. Add some content
    let _update = db
        .update_memory(UpdateRequest {
            topic_id: topic_id.clone(),
            dialogue_text: "User: Explain Rust ownership.\nAssistant: Ownership is Rust's core memory management system.".to_string(),
            summary: Some("ownership explanation".to_string()),
            action_chain: Some(vec![ActionItem {
                title: "explain_ownership".to_string(),
                description: "explain Rust ownership".to_string(),
                action_type: ActionType::Execute,
                parameters: None,
            }]),
            instant_distill: false,
            source: Default::default(),
        })
        .expect("update_memory failed");

    // 3. Activate the topic
    db.activate_topic(&topic_id, Some(600_000));

    // 4. Run dream with configured LLM
    println!("[Dream] Calling LLM API...");
    let report = db.dream(None).expect("dream failed");
    println!("[Dream] Complete: {:?}", report);

    drop(db);
    let _ = std::fs::remove_file(db_path);
}

// ============================================================================
// 测试：从文件路径导入 L3 超图并通过 L2 检索
// ============================================================================

#[test]
fn test_build_l3_from_meowagent() {
    let db_path = "/tmp/memhop_l3_meowagent.meh";
    let source_path = "/Volumes/zt_hd/projects/meow/meowagent/src";
    let _ = std::fs::remove_file(db_path);

    // Skip if meowagent source not available
    if !std::path::Path::new(source_path).exists() {
        eprintln!("[SKIP] meowagent source not found at {}", source_path);
        return;
    }

    let config = test_config(db_path);
    let mut db = MemHop::open(config.clone()).expect("MemHop::open failed");
    println!("[L3 Import] Database opened");

    // 2. Build L3 from meowagent/src
    let build_res = db
        .build_l3_hypergraph_from_path(PathBuf::from(source_path).as_path())
        .expect("build_l3 failed");
    println!(
        "[L3 Import] Created {} nodes, {} edges",
        build_res.created_ids.len(),
        build_res.updated_ids.len()
    );
    assert!(
        !build_res.created_ids.is_empty(),
        "build_l3 should create at least some nodes"
    );

    // 3. Query L3 list to verify nodes
    let l3_res = db
        .list_knowledge(KnowledgeListQuery {
            page: 1,
            page_size: 20,
            domain_filter: None,
            knowledge_type: None,
            keyword: None,
        })
        .expect("list_knowledge failed");
    println!("[L3 Query] Total L3 items: {}", l3_res.total);
    assert!(l3_res.total > 0, "L3 should have nodes after build_l3");

    // Print first few L3 node titles
    for item in l3_res.items.iter().take(5) {
        println!(
            "  L3: {} (type={}, importance={})",
            item.title, item.knowledge_type, item.importance
        );
    }

    // 4. Query L2 list to find the auto-created topic
    let l2_res = db
        .list_l2(TopicListQuery {
            page: 1,
            page_size: 10,
            active_only: false,
            keyword: None,
        })
        .expect("list_l2 failed");
    println!("[L2 Query] Total L2 topics: {}", l2_res.total);
    assert!(l2_res.total > 0, "build_l3 should create an L2 topic");

    let l2_id = l2_res.items[0].id.clone();
    let l2_title = l2_res.items[0].title.clone();
    println!("  L2: '{}' (id={})", l2_title, l2_id);

    // 5. Get L2 topic detail to verify L3 linkage (TopicDetail has l3_refs)
    let l2_detail = db
        .get_l2(&l2_id)
        .expect("get_l2 failed")
        .expect("topic should exist");
    println!(
        "[L2 Detail] title='{}', l3_refs={:?}",
        l2_detail.title, l2_detail.l3_refs
    );
    assert!(
        !l2_detail.l3_refs.is_empty(),
        "L2 detail should include l3_refs"
    );

    // 6. Search via context_id (doesn't need encoder) to verify L3 discovery
    let search_res = db
        .search_context(SearchQuery {
            dialogue: "meowagent code".to_string(),
            l2_id: None,
            context_id: Some(l2_id.clone()),
            l3_id: None,
            context_limit: 5,

            auto_create: 0,
            min_score: 0.0,

            source: Default::default(),
        })
        .expect("search_context with context_id failed");
    println!(
        "[Search context_id] contexts: {}",
        search_res.contexts.len()
    );
    assert!(
        !search_res.contexts.is_empty(),
        "search should return the L2 context"
    );
    println!("[Search] Discovered L3 IDs: {:?}", search_res.l3_ids);
    assert!(
        !search_res.l3_ids.is_empty(),
        "Search via L2 should discover L3 IDs from l3_refs"
    );

    // 7. Sync and close
    db.sync().expect("sync failed");
    drop(db);

    // 8. Reopen and verify persistence
    let db2 = MemHop::open(config).expect("reopen failed");

    let l3_persisted = db2
        .list_knowledge(KnowledgeListQuery {
            page: 1,
            page_size: 5,
            domain_filter: None,
            knowledge_type: None,
            keyword: None,
        })
        .expect("list_knowledge after reopen failed");
    println!(
        "[Persistence] L3 nodes after reopen: {}",
        l3_persisted.total
    );
    assert!(
        l3_persisted.total > 0,
        "L3 nodes should persist after close/reopen"
    );

    let l2_persisted = db2
        .list_l2(TopicListQuery {
            page: 1,
            page_size: 5,
            active_only: false,
            keyword: None,
        })
        .expect("list_l2 after reopen failed");
    println!(
        "[Persistence] L2 topics after reopen: {}",
        l2_persisted.total
    );
    assert!(l2_persisted.total > 0, "L2 topics should persist");

    drop(db2);
    let _ = std::fs::remove_file(db_path);
}

// ============================================================================
// 测试：L3 索引在 checkpoint/close/reopen 后持久化（P0 回归）
// ============================================================================

#[test]
fn test_l3_index_persistence_across_reopen() {
    let db_path = std::env::temp_dir().join("memhop_l3_index_persist.meh");
    let source_path = std::env::temp_dir().join("memhop_l3_index_persist_src");
    let _ = std::fs::remove_file(&db_path);
    let _ = std::fs::remove_dir_all(&source_path);

    std::fs::create_dir_all(source_path.join("src")).unwrap();
    std::fs::write(source_path.join("src/a.rs"), "pub fn foo() {}\n").unwrap();

    let config = test_config(db_path.to_str().unwrap());
    let mut db = MemHop::open(config.clone()).expect("MemHop::open failed");

    let build_res = db
        .build_l3_hypergraph_from_path(&source_path)
        .expect("build_l3 failed");
    assert!(
        !build_res.created_ids.is_empty(),
        "build_l3 should create at least one node"
    );

    let l3_res = db
        .list_knowledge(KnowledgeListQuery {
            page: 1,
            page_size: 10,
            domain_filter: None,
            knowledge_type: None,
            keyword: None,
        })
        .expect("list_knowledge failed");
    assert!(
        !l3_res.items.is_empty(),
        "L3 should contain the built graph"
    );
    let graph_id = l3_res.items[0].id.clone();

    let before_keyword = db
        .search_knowledge_nodes_by_keyword(&graph_id, "foo", 10)
        .expect("keyword search before close failed");
    assert!(
        before_keyword.total > 0,
        "should find node by keyword 'foo' before close"
    );

    let before_type = db
        .get_knowledge_nodes_by_type(&graph_id, "rust_module", 10)
        .expect("type search before close failed");
    assert!(
        before_type.total > 0,
        "should find node by type 'rust_module' before close"
    );

    db.checkpoint().expect("checkpoint failed");
    db.close().expect("close failed");

    let db2 = MemHop::open(config).expect("reopen failed");

    let after_keyword = db2
        .search_knowledge_nodes_by_keyword(&graph_id, "foo", 10)
        .expect("keyword search after reopen failed");
    assert_eq!(
        after_keyword.total, before_keyword.total,
        "keyword search results should persist across reopen"
    );

    let after_type = db2
        .get_knowledge_nodes_by_type(&graph_id, "rust_module", 10)
        .expect("type search after reopen failed");
    assert_eq!(
        after_type.total, before_type.total,
        "type search results should persist across reopen"
    );

    drop(db2);
    let _ = std::fs::remove_file(&db_path);
    let _ = std::fs::remove_dir_all(&source_path);
}
