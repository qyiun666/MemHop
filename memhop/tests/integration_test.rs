//! 集成测试 - 测试完整的存储→召回流程

use std::collections::HashMap;
use memhop::{Brain, BrainConfig, StoreBatch, StoreItem, RecallRequest, Layer};

/// 创建临时测试用 Brain 实例
fn make_test_brain() -> Brain {
    let tmp = tempfile::tempdir().unwrap();
    let cfg = BrainConfig {
        brains_dir: tmp.path().to_str().unwrap().to_string(),
        agent_id: "integration_test".to_string(),
        model_path: None,
    };
    Brain::open(cfg).unwrap()
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
        },
        StoreItem {
            text: "Machine learning with TensorFlow".to_string(),
            source: "chat".to_string(),
            turn_id: Some("turn_3".to_string()),
            session_id: Some("session_1".to_string()),
            topic_label: Some("ml".to_string()),
            llm_keywords: Some(vec!["machine learning".to_string(), "tensorflow".to_string()]),
            llm_compressed_summary: Some("ML framework".to_string()),
            valence: Some(0.6),
            arousal: Some(0.6),
            chain_parent_id: None,
            chain_label: None,
            domain_id: None,
            importance: Some(0.7),
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
    };
    
    let recall_result = brain.recall(&req);
    assert!(recall_result.is_ok());
    let resp = recall_result.unwrap();
    assert!(resp.results.len() > 0);
    
    // 验证召回的内容包含相关结果
    let texts: Vec<&str> = resp.results.iter()
        .map(|r| r.text.as_str())
        .collect();
    assert!(texts.iter().any(|t| t.contains("Rust") || t.contains("Python")));
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
    assert!(resp.results.len() == 10);
}

#[test]
fn test_consolidate_after_store() {
    let mut brain = make_test_brain();
    
    // 存储数据
    let items = vec![
        StoreItem {
            text: "Data for consolidation test".to_string(),
            source: "chat".to_string(),
            topic_label: Some("consolidation_topic".to_string()),
            ..Default::default()
        },
    ];
    
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
    let items = vec![
        StoreItem {
            text: "Time range test".to_string(),
            source: "chat".to_string(),
            ..Default::default()
        },
    ];
    
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
    assert!(result.is_ok());
    let resp = result.unwrap();
    assert_eq!(resp.results.len(), 0);
}

#[test]
fn test_session_management() {
    let mut brain = make_test_brain();
    
    // 激活话题
    brain.session_mgr.activate("test_session", "topic_1", 3600000);
    brain.session_mgr.activate("test_session", "topic_2", 3600000);
    
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
    println!("batch_store report: l2_topics_created={}", report.l2_topics_created);
    
    // 获取话题列表
    let topics = brain.list_topics().unwrap();
    println!("topics count: {}", topics.len());
    for topic in &topics {
        println!("  topic: id={}, label={}", topic.id, topic.label);
    }
    assert!(topics.len() > 0, "Expected at least one topic, got {}", topics.len());
    
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
        model_path: None,
    };
    
    let cfg2 = BrainConfig {
        brains_dir: tmp2.path().to_str().unwrap().to_string(),
        agent_id: "agent_2".to_string(),
        model_path: None,
    };
    
    let mut brain1 = Brain::open(cfg1).unwrap();
    let mut brain2 = Brain::open(cfg2).unwrap();
    
    // agent_1 存储数据
    let items = vec![
        StoreItem {
            text: "Agent 1 private data".to_string(),
            source: "chat".to_string(),
            ..Default::default()
        },
    ];
    brain1.batch_store(StoreBatch { items }).unwrap();
    
    // agent_2 查询应该找不到 agent_1 的数据
    let req = RecallRequest {
        query: "Agent 1".to_string(),
        max_results: 10,
        target_layers: vec![Layer::L1],
        ..Default::default()
    };
    
    let result = brain2.recall(&req);
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
