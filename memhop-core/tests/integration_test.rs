//! 集成测试 - 测试完整的存储→召回流程

use memhop_core::{Brain, BrainConfig, EmotionalFeedback, Emotion, HyperedgeKind, Layer, RecallRequest, StoreBatch, StoreItem};
use std::sync::Arc;

/// 创建临时测试用 Brain 实例
fn make_test_brain() -> Brain {
    let tmp = tempfile::tempdir().unwrap();
    let cfg = BrainConfig {
        brains_dir: tmp.path().to_str().unwrap().to_string(),
        agent_id: "integration_test".to_string(),
    };
    let encoder: Arc<Box<dyn memhop_core::Encoder>> = Arc::new(Box::new(memhop_core::NgramEncoder::new(1024)));
    Brain::open(cfg, encoder).unwrap()
}

#[test]
fn test_store_and_recall_basic() {
    let mut brain = make_test_brain();

    // 存储测试数据
    let items = vec![
        StoreItem {
            text: "Rust is a systems programming language".to_string(),
            source: "chat".to_string(),
            turn_id: Some("turn_1".to_string()),
            session_id: Some("session_1".to_string()),
            topic_label: Some("rust".to_string()),
            llm_keywords: Some(vec!["rust".to_string(), "programming".to_string()]),
            llm_compressed_summary: Some("Rust language overview".to_string()),
            valence: Some(0.8),
            arousal: Some(0.5),
            chain_parent_id: None,
            chain_label: None,
            domain_id: None,
            importance: Some(0.9),
            is_structural: None,
            source_ref: None,
            skeletal_text: None,
        },
        StoreItem {
            text: "Python is great for data science".to_string(),
            source: "chat".to_string(),
            turn_id: Some("turn_2".to_string()),
            session_id: Some("session_1".to_string()),
            topic_label: Some("python".to_string()),
            llm_keywords: Some(vec!["python".to_string(), "data science".to_string()]),
            llm_compressed_summary: Some("Python for data".to_string()),
            valence: Some(0.7),
            arousal: Some(0.4),
            chain_parent_id: None,
            chain_label: None,
            domain_id: None,
            importance: Some(0.8),
            is_structural: None,
            source_ref: None,
            skeletal_text: None,
        },
        StoreItem {
            text: "Machine learning with TensorFlow".to_string(),
            source: "chat".to_string(),
            turn_id: Some("turn_3".to_string()),
            session_id: Some("session_1".to_string()),
            topic_label: Some("ml".to_string()),
            llm_keywords: Some(vec![
                "machine learning".to_string(),
                "tensorflow".to_string(),
            ]),
            llm_compressed_summary: Some("ML framework".to_string()),
            valence: Some(0.6),
            arousal: Some(0.6),
            chain_parent_id: None,
            chain_label: None,
            domain_id: None,
            importance: Some(0.7),
            is_structural: None,
            source_ref: None,
            skeletal_text: None,
        },
    ];

    let store_result = brain.batch_store(StoreBatch { items });
    assert!(store_result.is_ok());
    let report = store_result.unwrap();
    // l1_nodes_created 表示 L1 层创建的节点数
    assert!(report.l1_nodes_created > 0);

    // 测试召回 - 相关查询
    let req = RecallRequest {
        query: "programming language".to_string(),
        max_results: 10,
        target_layers: vec![Layer::L1, Layer::L2],
        time_range: None,
        spread_depth: None,
        topic_filter: None,
        exclude_ids: vec![],
        exclude_topic_ids: vec![],
        l3_domain_id: None,
        l2_topic_id: None,
        session_id: None,
        time_decay_lambda: None,
        l3_max_domains: None,
        active_l3_domains: None,
    };

    let recall_result = brain.recall(&req);
    assert!(recall_result.is_ok());
    let resp = recall_result.unwrap();
    assert!(resp.results.len() > 0);

    // 验证召回的内容包含相关结果
    let texts: Vec<&str> = resp.results.iter().map(|r| r.text.as_str()).collect();
    assert!(
        texts
            .iter()
            .any(|t| t.contains("Rust") || t.contains("Python"))
    );
}

#[test]
fn test_store_and_recall_with_session_filter() {
    let mut brain = make_test_brain();

    // 存储不同会话的数据
    let items = vec![
        StoreItem {
            text: "Session 1 conversation".to_string(),
            source: "chat".to_string(),
            session_id: Some("session_1".to_string()),
            ..Default::default()
        },
        StoreItem {
            text: "Session 2 conversation".to_string(),
            source: "chat".to_string(),
            session_id: Some("session_2".to_string()),
            ..Default::default()
        },
    ];

    brain.batch_store(StoreBatch { items }).unwrap();

    // 测试按会话过滤 - 注意：session_id 是用于激活话题优先，不是过滤
    let req = RecallRequest {
        query: "conversation".to_string(),
        max_results: 10,
        target_layers: vec![Layer::L1],
        session_id: Some("session_1".to_string()),
        ..Default::default()
    };

    let result = brain.recall(&req);
    assert!(result.is_ok());
    let resp = result.unwrap();

    // 应该返回结果（session_id 用于激活话题优先，不是过滤）
    assert!(resp.results.len() > 0);
}

#[test]
fn test_store_and_recall_with_topic_filter() {
    let mut brain = make_test_brain();

    // 存储不同话题的数据
    let items = vec![
        StoreItem {
            text: "Rust language features".to_string(),
            source: "chat".to_string(),
            topic_label: Some("rust".to_string()),
            ..Default::default()
        },
        StoreItem {
            text: "Python libraries".to_string(),
            source: "chat".to_string(),
            topic_label: Some("python".to_string()),
            ..Default::default()
        },
    ];

    brain.batch_store(StoreBatch { items }).unwrap();

    // 获取话题列表
    let topics = brain.list_topics().unwrap();
    if let Some(topic) = topics.first() {
        // 测试按话题过滤 - 需要使用 topic_id
        let req = RecallRequest {
            query: "language".to_string(),
            max_results: 10,
            target_layers: vec![Layer::L1],
            topic_filter: Some(topic.id.clone()),
            ..Default::default()
        };

        let result = brain.recall(&req);
        assert!(result.is_ok());
    }
}

#[test]
fn test_batch_store_multiple_items() {
    let mut brain = make_test_brain();

    // 批量存储多条数据
    let items: Vec<StoreItem> = (0..10)
        .map(|i| StoreItem {
            text: format!("Test item {}", i),
            source: "batch_test".to_string(),
            turn_id: Some(format!("turn_{}", i)),
            session_id: Some("batch_session".to_string()),
            ..Default::default()
        })
        .collect();

    let result = brain.batch_store(StoreBatch { items });
    assert!(result.is_ok());
    let report = result.unwrap();
    assert!(report.l1_nodes_created > 0);

    // 验证所有数据都能被召回
    let req = RecallRequest {
        query: "Test item".to_string(),
        max_results: 20,
        target_layers: vec![Layer::L1],
        ..Default::default()
    };

    let recall_result = brain.recall(&req);
    assert!(recall_result.is_ok());
    let resp = recall_result.unwrap();
    // v0.23.1: 级联检索可能返回少于 10 个结果
    assert!(resp.results.len() > 0, "Expected at least 1 result, got {}", resp.results.len());
}

#[test]
fn test_consolidate_after_store() {
    let mut brain = make_test_brain();

    // 存储数据
    let items = vec![StoreItem {
        text: "Data for consolidation test".to_string(),
        source: "chat".to_string(),
        topic_label: Some("consolidation_topic".to_string()),
        ..Default::default()
    }];

    brain.batch_store(StoreBatch { items }).unwrap();

    // 执行整合
    let consolidate_result = brain.consolidate();
    assert!(consolidate_result.is_ok());

    // 整合后应该仍然能召回数据
    let req = RecallRequest {
        query: "consolidation".to_string(),
        max_results: 10,
        target_layers: vec![Layer::L1, Layer::L2],
        ..Default::default()
    };

    let recall_result = brain.recall(&req);
    assert!(recall_result.is_ok());
}

#[test]
fn test_recall_with_time_range() {
    let mut brain = make_test_brain();

    // 存储数据
    let items = vec![StoreItem {
        text: "Time range test".to_string(),
        source: "chat".to_string(),
        ..Default::default()
    }];

    brain.batch_store(StoreBatch { items }).unwrap();

    // 获取当前时间戳
    let now = chrono::Utc::now().timestamp_millis();
    let one_hour_ago = now - 3600000;
    let one_hour_later = now + 3600000;

    // 测试时间范围过滤
    let req = RecallRequest {
        query: "time range".to_string(),
        max_results: 10,
        target_layers: vec![Layer::L1],
        time_range: Some((one_hour_ago, one_hour_later)),
        ..Default::default()
    };

    let result = brain.recall(&req);
    assert!(result.is_ok());
}

#[test]
fn test_recall_empty_index() {
    let mut brain = make_test_brain();

    // 空索引查询应该返回空结果
    let req = RecallRequest {
        query: "empty test".to_string(),
        max_results: 10,
        target_layers: vec![Layer::L1],
        ..Default::default()
    };

    let result = brain.recall(&req);
    eprintln!("DEBUG test_recall_empty_index: {:?}", result);
    assert!(result.is_ok());
    let resp = result.unwrap();
    assert_eq!(resp.results.len(), 0);
}

#[test]
fn test_session_management() {
    let mut brain = make_test_brain();

    // 激活话题
    brain
        .session_mgr
        .activate("test_session", "topic_1", 3600000);
    brain
        .session_mgr
        .activate("test_session", "topic_2", 3600000);

    // 验证激活列表
    let active = brain.session_mgr.get_active_topic_ids("test_session");
    assert_eq!(active.len(), 2);
    assert!(active.contains(&"topic_1".to_string()));
    assert!(active.contains(&"topic_2".to_string()));

    // 停用一个话题
    brain.session_mgr.deactivate("test_session", "topic_1");

    let active_after = brain.session_mgr.get_active_topic_ids("test_session");
    assert_eq!(active_after.len(), 1);
    assert!(!active_after.contains(&"topic_1".to_string()));
}

#[test]
fn test_topic_extraction_and_update() {
    let mut brain = make_test_brain();

    // 存储带话题的数据
    let items = vec![
        StoreItem {
            text: "Rust programming language features".to_string(),
            source: "chat".to_string(),
            topic_label: Some("rust".to_string()),
            ..Default::default()
        },
        StoreItem {
            text: "Rust ownership and borrowing".to_string(),
            source: "chat".to_string(),
            topic_label: Some("rust".to_string()),
            ..Default::default()
        },
    ];

    let report = brain.batch_store(StoreBatch { items }).unwrap();
    println!(
        "batch_store report: l2_topics_created={}",
        report.l2_topics_created
    );

    // 获取话题列表
    let topics = brain.list_topics().unwrap();
    println!("topics count: {}", topics.len());
    for topic in &topics {
        println!("  topic: id={}, label={}", topic.id, topic.label);
    }
    assert!(
        topics.len() > 0,
        "Expected at least one topic, got {}",
        topics.len()
    );

    // 更新话题
    if let Some(topic) = topics.first() {
        let result = brain.update_topic(
            &topic.id,
            Some("Updated topic summary".to_string()),
            Some(vec!["updated".to_string()]),
            None,
        );
        assert!(result.is_ok());
    }
}

#[test]
fn test_multiple_agents_isolation() {
    // 测试不同 agent 的数据隔离
    let tmp1 = tempfile::tempdir().unwrap();
    let tmp2 = tempfile::tempdir().unwrap();

    let cfg1 = BrainConfig {
        brains_dir: tmp1.path().to_str().unwrap().to_string(),
        agent_id: "agent_1".to_string(),
    };

    let cfg2 = BrainConfig {
        brains_dir: tmp2.path().to_str().unwrap().to_string(),
        agent_id: "agent_2".to_string(),
    };

    let encoder: Arc<Box<dyn memhop_core::Encoder>> = Arc::new(Box::new(memhop_core::NgramEncoder::new(1024)));
    let mut brain1 = Brain::open(cfg1, encoder.clone()).unwrap();
    let mut brain2 = Brain::open(cfg2, encoder.clone()).unwrap();

    // agent_1 存储数据
    let items = vec![StoreItem {
        text: "Agent 1 private data".to_string(),
        source: "chat".to_string(),
        ..Default::default()
    }];
    brain1.batch_store(StoreBatch { items }).unwrap();

    // agent_2 查询应该找不到 agent_1 的数据
    let req = RecallRequest {
        query: "Agent 1".to_string(),
        max_results: 10,
        target_layers: vec![Layer::L1],
        ..Default::default()
    };

    let result = brain2.recall(&req);
    eprintln!("DEBUG test_multiple_agents_isolation: {:?}", result);
    assert!(result.is_ok());
    let resp = result.unwrap();
    assert_eq!(resp.results.len(), 0);
}

#[test]
fn test_large_batch_store() {
    let mut brain = make_test_brain();

    // 大批量存储测试
    let items: Vec<StoreItem> = (0..100)
        .map(|i| StoreItem {
            text: format!("Large batch item {} with some content", i),
            source: "batch_test".to_string(),
            ..Default::default()
        })
        .collect();

    let result = brain.batch_store(StoreBatch { items });
    assert!(result.is_ok());
    let report = result.unwrap();
    // l1_nodes_created 应该大于 0
    assert!(report.l1_nodes_created > 0);

    // 验证能召回数据
    let req = RecallRequest {
        query: "Large batch item".to_string(),
        max_results: 150,
        target_layers: vec![Layer::L1],
        ..Default::default()
    };

    let recall_result = brain.recall(&req);
    assert!(recall_result.is_ok());
    let resp = recall_result.unwrap();
    assert!(resp.results.len() > 0);
}

#[test]
fn test_recall_with_max_results() {
    let mut brain = make_test_brain();

    // 存储多条数据
    let items: Vec<StoreItem> = (0..50)
        .map(|i| StoreItem {
            text: format!("Result limit test item {}", i),
            source: "test".to_string(),
            ..Default::default()
        })
        .collect();

    brain.batch_store(StoreBatch { items }).unwrap();

    // 测试限制返回数量
    let req = RecallRequest {
        query: "Result limit test".to_string(),
        max_results: 5,
        target_layers: vec![Layer::L1],
        ..Default::default()
    };

    let result = brain.recall(&req);
    assert!(result.is_ok());
    let resp = result.unwrap();
    assert!(resp.results.len() <= 5);
}

#[test]
fn test_procedural_crystallization() {
    let mut brain = make_test_brain();

    // 存储 3 条带相同 chain_label 的 chain 数据
    let items = vec![
        StoreItem {
            text: "第一步：确认错误信息".to_string(),
            source: "chat".to_string(),
            topic_label: Some("错误排查".to_string()),
            ..Default::default()
        },
        StoreItem {
            text: "第二步：定位错误原因".to_string(),
            source: "chat".to_string(),
            topic_label: Some("错误排查".to_string()),
            ..Default::default()
        },
        StoreItem {
            text: "第三步：修复并验证".to_string(),
            source: "chat".to_string(),
            topic_label: Some("错误排查".to_string()),
            ..Default::default()
        },
    ];

    let report = brain.batch_store(StoreBatch { items }).unwrap();
    assert!(report.l1_nodes_created > 0);

    // 获取节点 ID
    let node0 = report.engram_ids.get("0").cloned().unwrap_or_default();
    let node1 = report.engram_ids.get("1").cloned().unwrap_or_default();
    let node2 = report.engram_ids.get("2").cloned().unwrap_or_default();
    assert!(!node0.is_empty() && !node1.is_empty() && !node2.is_empty(),
        "Expected all 3 node IDs to be present");

    // 直接创建 3 条独立的超边链，每条都带 chain_label "错误排查"
    // 注意：add_hyperedge 用 timestamp_millis() 生成 ID，需要增加延迟避免 ID 冲突
    {
        use std::time::Duration;
        brain.ensure_l1().unwrap();
        let store = brain.redb_store.as_ref().unwrap();
        let mut wtxn = store.begin_write().unwrap();
        let l1 = brain.l1.as_mut().unwrap();

        std::thread::sleep(Duration::from_millis(2));
        let h1 = l1.add_hyperedge(
            store, &mut wtxn,
            vec![node0.clone()],
            HyperedgeKind::Evolution, 1.0,
            None, Some("错误排查".to_string()),
        ).unwrap();
        std::thread::sleep(Duration::from_millis(2));
        let _ = l1.add_hyperedge(
            store, &mut wtxn,
            vec![node0.clone()],
            HyperedgeKind::Evolution, 1.0,
            Some(h1), Some("错误排查".to_string()),
        ).unwrap();

        std::thread::sleep(Duration::from_millis(2));
        let h2 = l1.add_hyperedge(
            store, &mut wtxn,
            vec![node1.clone()],
            HyperedgeKind::Evolution, 1.0,
            None, Some("错误排查".to_string()),
        ).unwrap();
        std::thread::sleep(Duration::from_millis(2));
        let _ = l1.add_hyperedge(
            store, &mut wtxn,
            vec![node1.clone()],
            HyperedgeKind::Evolution, 1.0,
            Some(h2), Some("错误排查".to_string()),
        ).unwrap();

        std::thread::sleep(Duration::from_millis(2));
        let h3 = l1.add_hyperedge(
            store, &mut wtxn,
            vec![node2.clone()],
            HyperedgeKind::Evolution, 1.0,
            None, Some("错误排查".to_string()),
        ).unwrap();
        std::thread::sleep(Duration::from_millis(2));
        let _ = l1.add_hyperedge(
            store, &mut wtxn,
            vec![node2.clone()],
            HyperedgeKind::Evolution, 1.0,
            Some(h3), Some("错误排查".to_string()),
        ).unwrap();

        wtxn.commit().unwrap();
    }

    // 调用 crystallize
    let crystal_report = brain.procedural_crystallize().unwrap();
    assert!(crystal_report.crystals_created >= 1,
        "Expected at least 1 crystal, got {}", crystal_report.crystals_created);

    // 验证 list_crystals 返回至少 1 个晶体
    let crystals = brain.list_crystals().unwrap();
    assert!(crystals.len() >= 1,
        "Expected at least 1 crystal, got {}", crystals.len());

    // 验证晶体具有正确的内容
    if let Some(crystal) = crystals.first() {
        assert!(crystal.label.contains("错误排查"));
        assert!(!crystal.trigger_keywords.is_empty());
    }

    // 调用 recall，验证 recommended_crystals 非空
    let req = RecallRequest {
        query: "错误排查".to_string(),
        ..Default::default()
    };
    let resp = brain.recall(&req).unwrap();
    assert!(resp.recommended_crystals.len() >= 1,
        "Expected non-empty recommended_crystals");
}

#[test]
fn test_get_emotion_nonexistent() {
    let mut brain = make_test_brain();

    // 对不存在的 memory_id 调用 get_emotion 应返回 Err
    let result = brain.get_emotion("nonexistent-memory-id");
    assert!(result.is_err(), "Expected Err for nonexistent memory_id");
    let err = result.unwrap_err();
    let err_msg = format!("{}", err);
    assert!(
        err_msg.contains("emotion not found"),
        "Error message should contain 'emotion not found', got: {}",
        err_msg
    );
}

#[test]
fn test_get_emotion_after_feedback() {
    let mut brain = make_test_brain();

    // 存储一条记忆
    let items = vec![StoreItem {
        text: "Emotion test memory".to_string(),
        source: "test".to_string(),
        ..Default::default()
    }];
    let report = brain.batch_store(StoreBatch { items }).unwrap();
    assert!(report.l1_nodes_created > 0);

    // 从 report 中获取 engam ID
    let memory_id = report.engram_ids.get("0").cloned().unwrap();
    assert!(!memory_id.is_empty(), "Expected non-empty memory_id");

    // 施加情感反馈
    let feedback = EmotionalFeedback {
        memory_id: memory_id.clone(),
        emotion: Emotion::Joy,
        intensity: 0.8,
        reason: Some("positive".to_string()),
    };
    let fb_result = brain.emotional_feedback(&feedback);
    assert!(fb_result.is_ok(), "emotional_feedback failed: {:?}", fb_result);

    // 获取情感维度，验证与反馈一致
    let emo = brain.get_emotion(&memory_id).unwrap();
    assert_eq!(
        emo.emotion, Emotion::Joy,
        "Expected Joy, got {:?}",
        emo.emotion
    );
    assert!(
        (emo.intensity - 0.8).abs() < 1e-6,
        "Expected intensity 0.8, got {}",
        emo.intensity
    );
}

#[test]
fn test_set_l0_and_get_profile() {
    let mut brain = make_test_brain();

    brain
        .set_l0(
            Some("catid_test".to_string()),
            Some("test_role".to_string()),
            vec!["curious".to_string(), "helpful".to_string()],
            vec!["truth".to_string()],
            vec!["open_source".to_string()],
            std::collections::HashMap::new(),
        )
        .unwrap();

    let profile = brain.get_l0_profile().unwrap().unwrap();
    assert_eq!(profile.catid, Some("catid_test".to_string()));
    assert_eq!(profile.role_name, Some("test_role".to_string()));
    assert!(profile.personality.contains(&"curious".to_string()));
    assert!(profile.values.contains(&"truth".to_string()));
}

