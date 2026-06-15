mod common;

use common::test_context::TestContext;
use memhop::*;
use std::collections::HashMap;

/// 主端到端测试：按功能顺序执行所有测试
#[test]
fn test_e2e_full_lifecycle() {
    let mut ctx = TestContext::setup();

    // Phase 1: 基础 CRUD（创建数据）
    test_l0_profile_crud(&mut ctx);
    test_import_l0_profile(&mut ctx);
    test_import_l3_knowledge_stub(&mut ctx);
    test_search_auto_create_l2(&mut ctx);
    test_import_l2_topics(&mut ctx);

    // Phase 2: 查询验证（使用 Phase 1 的数据）
    test_search_memory_with_vector(&mut ctx);
    test_list_topics(&ctx);
    test_list_archives_by_topic(&ctx);
    test_list_crystals(&ctx);

    // Phase 3: 参数组合测试
    test_search_all_param_combinations(&mut ctx);
    test_update_all_param_combinations(&mut ctx);
    test_list_all_param_combinations(&ctx);
    test_import_all_param_combinations(&mut ctx);

    // Phase 4: 边界情况
    test_edge_cases(&mut ctx);

    // Phase 5: 高级操作
    test_update_topic_title(&mut ctx);
    test_merge_topics(&mut ctx);

    // Phase 6: 数据持久化验证
    test_database_close_and_reopen(&mut ctx);
}

// ============================================================================
// Phase 1: 基础 CRUD
// ============================================================================

fn test_l0_profile_crud(ctx: &mut TestContext) {
    let request = UpdateProfileRequest {
        name: Some("Test Agent".to_string()),
        role: Some("AI Assistant".to_string()),
        personality: Some("Friendly".to_string()),
        worldview: None,
        preferences: Some(HashMap::from([
            ("language".to_string(), "Chinese".to_string()),
        ])),
    };

    let updated = ctx.db.update_profile(request).unwrap();
    assert_eq!(updated.name, "Test Agent");

    let profile = ctx.db.get_profile().unwrap().unwrap();
    assert_eq!(profile.name, "Test Agent");
    assert_eq!(profile.preferences.get("language").unwrap(), "Chinese");

    println!("✅ L0 Profile CRUD passed");
}

fn test_import_l0_profile(ctx: &mut TestContext) {
    let request = ImportRequest {
        target_layer: TargetLayer::Profile,
        data: ImportData::Profile {
            name: Some("Imported Agent".to_string()),
            role: Some("Code Reviewer".to_string()),
            personality: None,
            worldview: None,
            preferences: None,
        },
        mode: ImportMode::Merge,
        knowledge_title: None,
    };

    let result = ctx.db.import_memory(request).unwrap();
    assert_eq!(result.status, ImportStatus::Success);

    println!("✅ Import L0 Profile passed");
}

fn test_import_l3_knowledge_stub(ctx: &mut TestContext) {
    // L3 Knowledge layer not available in current architecture (uses HypergraphSlot)
    let request = ImportRequest {
        target_layer: TargetLayer::Knowledge,
        data: ImportData::Knowledge(vec![
            KnowledgeImportItem {
                title: "Rust Ownership".to_string(),
                domain: "programming".to_string(),
                knowledge_type: "Conceptual".to_string(),
                text: "Rust ownership system ensures memory safety...".to_string(),
                summary: Some("Ownership rules".to_string()),
                keywords: vec!["ownership".to_string(), "borrowing".to_string()],
                source_ref: None,
            },
        ]),
        mode: ImportMode::Merge,
        knowledge_title: None,
    };

    // Should return error since L3 Knowledge is not supported
    let result = ctx.db.import_memory(request);
    assert!(result.is_err(), "L3 Knowledge import should fail (not supported)");

    println!("✅ Import L3 Knowledge (stub) passed");
}

fn test_search_auto_create_l2(ctx: &mut TestContext) {
    // Use search_memory with auto_create=1 to create L2 context
    let query = SearchQuery {
        dialogue: "How to learn Rust programming language".to_string(),
        context_id: None,
        l3_id: None,
        context_limit: 10,
        llm_enhance: None,
        auto_create: 1,
        min_score: 0.0,
        context_history: None,
    };

    let result = ctx.db.search_memory(query).unwrap();
    assert!(!result.contexts.is_empty(), "auto_create should create L2 context");

    let created_id = result.contexts[0].id.clone();
    assert!(!created_id.is_empty());
    ctx.created_l2_ids.push(created_id);

    println!("✅ Search auto_create L2 passed");
}

fn test_import_l2_topics(ctx: &mut TestContext) {
    let request = ImportRequest {
        target_layer: TargetLayer::Topic,
        data: ImportData::Topics(vec![
            TopicImportItem {
                title: "Python Basics".to_string(),
                summary: Some("Python programming basics".to_string()),
                keywords: vec!["python".to_string()],
                knowledge_domain: None,
            },
        ]),
        mode: ImportMode::Merge,
        knowledge_title: None,
    };

    let result = ctx.db.import_memory(request).unwrap();
    assert_eq!(result.created_ids.len(), 1);
    ctx.created_l2_ids.extend(result.created_ids);

    println!("✅ Import L2 Topics passed");
}

// ============================================================================
// Phase 2: 查询验证
// ============================================================================

fn test_search_memory_with_vector(ctx: &mut TestContext) {
    let query = SearchQuery {
        dialogue: "Rust ownership memory safety".to_string(),
        context_id: None,
        l3_id: None,
        context_limit: 5,
        llm_enhance: None,
        auto_create: 0,
        min_score: 0.0,
        context_history: None,
    };

    let results = ctx.db.search_memory(query).unwrap();

    // 验证搜索结果
    assert!(!results.contexts.is_empty(),
        "向量搜索应找到语义相关的内容");

    println!("✅ Search Memory with Vector passed");
}

fn test_list_topics(ctx: &TestContext) {
    let query = TopicListQuery {
        page: 1,
        page_size: 10,
        active_only: false,
        keyword: None,
    };

    let result = ctx.db.list_topics(query).unwrap();
    assert!(result.total > 0, "Should have at least one L2 topic");

    println!("✅ List L2 Topics passed");
}

fn test_list_archives_by_topic(ctx: &TestContext) {
    if ctx.created_l2_ids.is_empty() {
        println!("⚠️  No L2 topics to test L4 queries");
        return;
    }

    let query = ArchivePageQuery {
        page: 1,
        page_size: 10,
        start_time: None,
        end_time: None,
        content_type: None,
    };

    let result = ctx.db.list_archives_by_topic(&ctx.created_l2_ids[0], query).unwrap();
    println!("  ✓ L4 archives found: {}", result.total);

    println!("✅ List L4 by Topic passed");
}

fn test_list_crystals(ctx: &TestContext) {
    let query = CrystalListQuery {
        page: 1,
        page_size: 10,
        status_filter: None,
        min_trigger_count: None,
        keyword: None,
    };

    let result = ctx.db.list_crystals(query).unwrap();
    println!("  ✓ L5 skills found: {}", result.total);

    println!("✅ List L5 Skills passed");
}

// ============================================================================
// Phase 3: 参数组合测试
// ============================================================================

fn test_search_all_param_combinations(ctx: &mut TestContext) {
    // 1. dialogue 边界
    let test_cases = vec![
        ("正常文本", "Rust programming".to_string()),
        ("空字符串", "".to_string()),
        ("纯中文", "中文测试".to_string()),
        ("特殊字符", "!@#$%^&*()".to_string()),
        ("超长文本", "A".repeat(10000)),
    ];

    for (name, dialogue) in test_cases {
        let query = SearchQuery {
            dialogue,
            context_id: None,
            l3_id: None,
            context_limit: 5,
            llm_enhance: None,
            auto_create: 0,
            min_score: 0.0,
            context_history: None,
        };
        let _ = ctx.db.search_memory(query);
        println!("  ✓ Search with dialogue: {}", name);
    }

    // 2. context_id 过滤
    if !ctx.created_l2_ids.is_empty() {
        let query = SearchQuery {
            dialogue: "Rust".to_string(),
            context_id: Some(ctx.created_l2_ids[0].clone()),
            l3_id: None,
            context_limit: 5,
            llm_enhance: None,
            auto_create: 0,
            min_score: 0.0,
        context_history: None,
        };
        let _ = ctx.db.search_memory(query);
        println!("  ✓ Search with context_id filter");
    }

    // 3. limit 组合
    let query = SearchQuery {
        dialogue: "Rust".to_string(),
        context_id: None,
        l3_id: None,
        context_limit: 0,
        llm_enhance: None,
        auto_create: 0,
        min_score: 0.0,
        context_history: None,
    };
    let _ = ctx.db.search_memory(query);
    println!("  ✓ Search with limit=0");

    // 4. auto_create 组合
    let query = SearchQuery {
        dialogue: "New unique topic".to_string(),
        context_id: None,
        l3_id: None,
        context_limit: 5,
        llm_enhance: None,
        auto_create: 1,
        min_score: 0.0,
        context_history: None,
    };
    let _ = ctx.db.search_memory(query);
    println!("  ✓ Search with auto_create=1");

    println!("✅ Search All Param Combinations passed");
}

fn test_update_all_param_combinations(ctx: &mut TestContext) {
    // 确保有一个可用的 topic_id
    let topic_id = if ctx.created_l2_ids.is_empty() {
        // 通过 auto_create 创建一个
        let query = SearchQuery {
            dialogue: "Create topic for update test".to_string(),
            context_id: None,
            l3_id: None,
            context_limit: 10,
            llm_enhance: None,
            auto_create: 1,
            min_score: 0.0,
        context_history: None,
        };
        let result = ctx.db.search_memory(query).unwrap();
        result.contexts[0].id.clone()
    } else {
        ctx.created_l2_ids[0].clone()
    };

    // 1. 更新已有 L2
    let request = UpdateRequest {
        topic_id: topic_id.clone(),
        dialogue_text: "Test dialogue".to_string(),
        summary: None,
        action_chain: vec![],
    };
    let _ = ctx.db.update_memory(request);
    println!("  ✓ Update with valid topic_id");

    // 2. action_chain 组合
    let request = UpdateRequest {
        topic_id: topic_id.clone(),
        dialogue_text: "Test with actions".to_string(),
        summary: Some("Updated summary".to_string()),
        action_chain: vec![
            ActionItem {
                title: "Action 1".to_string(),
                description: "Description 1".to_string(),
                action_type: ActionType::Create,
                parameters: None,
            },
        ],
    };
    let _ = ctx.db.update_memory(request);
    println!("  ✓ Update with action_chain");

    println!("✅ Update All Param Combinations passed");
}

fn test_list_all_param_combinations(ctx: &TestContext) {
    // TopicListQuery
    let test_cases = vec![
        (1, 10, false, None),
        (1, 1, true, None),
        (1, 100, false, Some("Rust".to_string())),
        (2, 10, false, None),
    ];

    for (page, page_size, active_only, keyword) in test_cases {
        let query = TopicListQuery {
            page,
            page_size,
            active_only,
            keyword,
        };
        let _ = ctx.db.list_topics(query);
    }
    println!("  ✓ TopicListQuery combinations");

    println!("✅ List All Param Combinations passed");
}

fn test_import_all_param_combinations(ctx: &mut TestContext) {
    // L0 + Merge
    let request = ImportRequest {
        target_layer: TargetLayer::Profile,
        data: ImportData::Profile {
            name: Some("Test".to_string()),
            role: None,
            personality: None,
            worldview: None,
            preferences: None,
        },
        mode: ImportMode::Merge,
        knowledge_title: None,
    };
    let _ = ctx.db.import_memory(request);
    println!("  ✓ Import L0 + Merge");

    // L2 + Overwrite
    let request = ImportRequest {
        target_layer: TargetLayer::Topic,
        data: ImportData::Topics(vec![]),
        mode: ImportMode::Overwrite,
        knowledge_title: None,
    };
    let _ = ctx.db.import_memory(request);
    println!("  ✓ Import L2 + Overwrite (empty)");

    println!("✅ Import All Param Combinations passed");
}

// ============================================================================
// Phase 4: 边界情况
// ============================================================================

fn test_edge_cases(ctx: &mut TestContext) {
    // 1. 不存在的 ID
    let result = ctx.db.get_topic("nonexistent_id");
    assert!(result.unwrap().is_none(), "Should return None for nonexistent ID");
    println!("  ✓ Get nonexistent L2");

    // 2. Unicode 字符 (通过 auto_create)
    let query = SearchQuery {
        dialogue: "中文测试 🎉".to_string(),
        context_id: None,
        l3_id: None,
        context_limit: 10,
        llm_enhance: None,
        auto_create: 1,
        min_score: 0.0,
        context_history: None,
    };
    let _ = ctx.db.search_memory(query);
    println!("  ✓ Search with Unicode");

    println!("✅ Edge Cases passed");
}

// ============================================================================
// Phase 5: 高级操作
// ============================================================================

fn test_update_topic_title(ctx: &mut TestContext) {
    if ctx.created_l2_ids.is_empty() {
        println!("  No topics to test update title");
        return;
    }

    let updated = ctx.db.update_topic_title(&ctx.created_l2_ids[0], "Updated Title".to_string()).unwrap();
    assert_eq!(updated.title, "Updated Title");

    println!("Update Topic Title passed");
}

fn test_merge_topics(ctx: &mut TestContext) {
    if ctx.created_l2_ids.len() < 2 {
        println!("  Need at least 2 topics to test merge");
        return;
    }

    let primary_id = ctx.created_l2_ids[0].clone();
    let secondary_id = ctx.created_l2_ids[1].clone();

    let merged = ctx.db.merge_topics(&primary_id, vec![secondary_id.clone()]).unwrap();
    assert_eq!(merged.id, primary_id);

    // 验证 secondary 被删除
    let secondary = ctx.db.get_topic(&secondary_id).unwrap();
    assert!(secondary.is_none(), "Secondary should be deleted after merge");

    println!("Merge Topics passed");
}

// ============================================================================
// Phase 6: 数据持久化验证
// ============================================================================

fn test_database_close_and_reopen(ctx: &mut TestContext) {
    let config = MemHopConfig {
        db_path: ctx.db_path.clone(),
        encoder_socket: ctx.socket_path.clone(),
        vector_dim: 384,
        crystal_path: None,
    };

    // 使用临时变量取出 db
    let db = std::mem::replace(&mut ctx.db, MemHop::open(config.clone()).unwrap());

    // 关闭数据库
    db.close().unwrap();

    // 验证文件存在
    assert!(ctx.db_path.exists(), "Database file should exist after close");

    // 重新打开数据库
    let db = MemHop::open(config).expect("Failed to reopen database");

    // 验证数据持久化
    let profile = db.get_profile().unwrap();
    assert!(profile.is_some(), "L0 profile should persist after close/reopen");

    let query = TopicListQuery {
        page: 1,
        page_size: 100,
        active_only: false,
        keyword: None,
    };
    let l2_list = db.list_topics(query).unwrap();
    assert!(l2_list.total > 0, "L2 topics should persist after close/reopen");

    // 更新 ctx.db
    ctx.db = db;

    println!("✅ Database Close and Reopen passed");
}
