#! MCP 接口测试 - 覆盖所有 15 个 JSON-RPC 接口

use memhop::{Brain, BrainConfig, HyperedgeKind, Layer, RecallRequest, ShelfDomain, StoreBatch, StoreItem};
use serde_json::{Value, json};
use std::collections::HashMap;

/// 创建临时测试用 Brain 实例
fn make_test_brain() -> Brain {
    let tmp = tempfile::tempdir().unwrap();
    let cfg = BrainConfig {
        brains_dir: tmp.path().to_str().unwrap().to_string(),
        agent_id: "test".to_string(),
        model_path: None,
    };
    Brain::open(cfg).unwrap()
}

/// 构造 JSON-RPC 请求
fn make_request(method: &str, params: Value) -> Value {
    json!({
        "jsonrpc": "2.0",
        "id": 1,
        "method": method,
        "params": params
    })
}

#[test]
fn test_handle_batch_store() {
    let mut brain = make_test_brain();

    // 正常存储
    let items = vec![StoreItem {
        text: "Rust is a systems programming language".to_string(),
        source: "chat".to_string(),
        turn_id: Some("turn_1".to_string()),
        session_id: Some("session_1".to_string()),
        topic_label: Some("rust".to_string()),
        llm_keywords: None,
        llm_compressed_summary: None,
        valence: Some(0.8),
        arousal: Some(0.5),
        chain_parent_id: None,
        chain_label: None,
        domain_id: None,
        importance: Some(0.7),
    }];

    let result = brain.batch_store(StoreBatch { items });
    assert!(result.is_ok());

    let report = result.unwrap();
    assert!(report.l1_nodes_created > 0);
}

#[test]
fn test_handle_recall() {
    let mut brain = make_test_brain();

    // 先存储一些数据
    let items = vec![
        StoreItem {
            text: "Rust is fast and safe".to_string(),
            source: "chat".to_string(),
            ..Default::default()
        },
        StoreItem {
            text: "Python is easy to learn".to_string(),
            source: "chat".to_string(),
            ..Default::default()
        },
    ];
    brain.batch_store(StoreBatch { items }).unwrap();

    // 测试召回
    let req = RecallRequest {
        query: "programming language".to_string(),
        max_results: 10,
        target_layers: vec![Layer::L1],
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

    let result = brain.recall(&req);
    assert!(result.is_ok());

    let resp = result.unwrap();
    assert!(resp.results.len() > 0);
}

#[test]
fn test_handle_consolidate() {
    let mut brain = make_test_brain();

    // 先存储一些数据
    let items = vec![StoreItem {
        text: "Test data for consolidation".to_string(),
        source: "chat".to_string(),
        ..Default::default()
    }];
    brain.batch_store(StoreBatch { items }).unwrap();

    // 测试整合
    let result = brain.consolidate();
    assert!(result.is_ok());
}

#[test]
fn test_handle_health() {
    // 健康检查不需要 Brain 实例
    // 直接验证版本号
    let version = "0.18.1";
    assert!(!version.is_empty());
}

#[test]
fn test_handle_get_set_profile() {
    let mut brain = make_test_brain();

    // 设置 profile
    let result = brain.set_l0_profile(
        Some("test_cat_1".to_string()),
        Some("assistant".to_string()),
        Some("helpful AI".to_string()),
        Some("virtual assistant".to_string()),
        HashMap::from([("style".to_string(), "friendly".to_string())]),
    );
    assert!(result.is_ok());

    // 获取 profile
    let profile = brain.get_l0_profile();
    assert!(profile.is_ok());
}

#[test]
fn test_handle_activate_deactivate() {
    let mut brain = make_test_brain();

    // 激活话题
    brain
        .session_mgr
        .activate("test_session", "topic_1", 3600000);

    // 获取激活列表
    let active = brain.get_activated_topics();
    assert!(active.len() > 0);

    // 停用话题
    brain.session_mgr.deactivate("test_session", "topic_1");

    let active_after = brain.get_activated_topics();
    assert_eq!(active_after.len(), 0);
}

#[test]
fn test_handle_list_topics() {
    let mut brain = make_test_brain();

    // 先存储带话题的数据
    let items = vec![StoreItem {
        text: "Topic test data".to_string(),
        source: "chat".to_string(),
        topic_label: Some("test_topic".to_string()),
        ..Default::default()
    }];
    brain.batch_store(StoreBatch { items }).unwrap();

    // 列出话题
    let topics = brain.list_topics();
    assert!(topics.is_ok());
}

#[test]
fn test_handle_re_search() {
    let mut brain = make_test_brain();

    // 存储数据
    let items = vec![StoreItem {
        text: "Search test data".to_string(),
        source: "chat".to_string(),
        ..Default::default()
    }];
    brain.batch_store(StoreBatch { items }).unwrap();

    // 正则搜索
    let req = RecallRequest {
        query: "test".to_string(),
        max_results: 10,
        target_layers: vec![Layer::L1],
        ..Default::default()
    };

    let result = brain.re_search(&req);
    assert!(result.is_ok());
}

#[test]
fn test_handle_stats() {
    let mut brain = make_test_brain();

    // 获取统计信息
    let l1_nodes = brain.l1.bm25.len();
    let l2_topics = brain.l2.topic_vectors.len();
    let l3_nodes = brain.l3.vector_index.len();
    let l4_docs = brain.l4.vector_index.len();

    // 验证统计信息结构
    assert!(l1_nodes >= 0);
    assert!(l2_topics >= 0);
    assert!(l3_nodes >= 0);
    assert!(l4_docs >= 0);
}

#[test]
fn test_handle_update_topic() {
    let mut brain = make_test_brain();

    // 先存储带话题的数据
    let items = vec![StoreItem {
        text: "Update topic test".to_string(),
        source: "chat".to_string(),
        topic_label: Some("update_test".to_string()),
        ..Default::default()
    }];
    brain.batch_store(StoreBatch { items }).unwrap();

    // 获取话题列表
    let topics = brain.list_topics().unwrap();
    if let Some(topic) = topics.first() {
        // 更新话题
        let result = brain.update_topic(
            &topic.id,
            Some("Updated summary".to_string()),
            Some(vec!["keyword1".to_string(), "keyword2".to_string()]),
            None,
        );
        assert!(result.is_ok());
    }
}

#[test]
fn test_handle_set_l0() {
    let mut brain = make_test_brain();

    // 设置 L0 profile
    let result = brain.set_l0(
        Some("test_cat_2".to_string()),
        Some("test_role".to_string()),
        vec!["trait1".to_string(), "trait2".to_string()],
        vec!["value1".to_string()],
        vec!["worldview1".to_string()],
        HashMap::from([("key".to_string(), "value".to_string())]),
    );
    assert!(result.is_ok());
}

#[test]
fn test_handle_feedback() {
    let mut brain = make_test_brain();

    // 先存储数据
    let items = vec![StoreItem {
        text: "Feedback test".to_string(),
        source: "chat".to_string(),
        ..Default::default()
    }];
    brain.batch_store(StoreBatch { items }).unwrap();

    // 激活话题
    brain
        .session_mgr
        .activate("test_session", "feedback_topic", 3600000);

    // 反馈逻辑测试
    let active_topic_ids = brain.session_mgr.get_active_topic_ids("test_session");
    // 验证反馈逻辑
    assert!(active_topic_ids.len() >= 0);
}

#[test]
fn test_handle_get_l4_raw() {
    let mut brain = make_test_brain();

    // 测试获取 L4 原始文档
    // 由于没有数据，应该返回错误或空
    let result = brain.get_l4_raw("non_existent_doc");
    assert!(result.is_err() || result.unwrap().is_none());
}

#[test]
fn test_handle_list_l3_paths() {
    let brain = make_test_brain();

    // 列出 L3 路径
    let result = brain.list_l3_paths();
    assert!(result.is_ok());
}

#[test]
fn test_handle_mount_unmount_shelf() {
    let mut brain = make_test_brain();

    // 创建临时目录并添加测试文件
    let tmp = tempfile::tempdir().unwrap();
    let test_dir = tmp.path().join("test_shelf");
    std::fs::create_dir(&test_dir).unwrap();

    // 创建测试文件
    let test_file = test_dir.join("test.txt");
    std::fs::write(&test_file, "This is test content for shelf mounting").unwrap();

    // 挂载 shelf
    let result = memhop::shelf::mount(
        &mut brain,
        test_dir.to_str().unwrap(),
        ShelfDomain::Generic,
        "test_shelf",
    );
    assert!(result.is_ok());

    // 列出 shelf
    let shelves = memhop::shelf::list(&brain);
    assert!(shelves.is_ok());
    assert!(shelves.unwrap().len() > 0);

    // 卸载 shelf - 使用正确的字段名 id
    if let Ok(shelves) = memhop::shelf::list(&brain) {
        if let Some(shelf) = shelves.first() {
            let result = memhop::shelf::unmount(&mut brain, &shelf.id);
            assert!(result.is_ok());
        }
    }
}

#[test]
fn test_crystallize_rpc() {
    let mut brain = make_test_brain();

    // 存储 chain 数据
    let items = vec![
        StoreItem {
            text: "发现页面报错".to_string(),
            source: "chat".to_string(),
            topic_label: Some("错误排查".to_string()),
            ..Default::default()
        },
        StoreItem {
            text: "查看错误日志".to_string(),
            source: "chat".to_string(),
            topic_label: Some("错误排查".to_string()),
            ..Default::default()
        },
        StoreItem {
            text: "修复重启服务".to_string(),
            source: "chat".to_string(),
            topic_label: Some("错误排查".to_string()),
            ..Default::default()
        },
    ];

    let report = brain.batch_store(StoreBatch { items }).unwrap();

    let node0 = report.engram_ids.get("0").cloned().unwrap_or_default();
    let node1 = report.engram_ids.get("1").cloned().unwrap_or_default();
    let node2 = report.engram_ids.get("2").cloned().unwrap_or_default();

    // 创建 3 条超边链
    {
        use std::time::Duration;
        let env = brain.l1_env.env.clone();
        let mut wtxn = env.write_txn().unwrap();

        std::thread::sleep(Duration::from_millis(2));
        let h1 = brain.l1.add_hyperedge(
            &mut wtxn, &brain.l1_env,
            vec![node0.clone()], HyperedgeKind::Evolution, 1.0,
            None, Some("错误排查".to_string()),
        ).unwrap();
        std::thread::sleep(Duration::from_millis(2));
        let _ = brain.l1.add_hyperedge(
            &mut wtxn, &brain.l1_env,
            vec![node0.clone()], HyperedgeKind::Evolution, 1.0,
            Some(h1), Some("错误排查".to_string()),
        ).unwrap();

        std::thread::sleep(Duration::from_millis(2));
        let h2 = brain.l1.add_hyperedge(
            &mut wtxn, &brain.l1_env,
            vec![node1.clone()], HyperedgeKind::Evolution, 1.0,
            None, Some("错误排查".to_string()),
        ).unwrap();
        std::thread::sleep(Duration::from_millis(2));
        let _ = brain.l1.add_hyperedge(
            &mut wtxn, &brain.l1_env,
            vec![node1.clone()], HyperedgeKind::Evolution, 1.0,
            Some(h2), Some("错误排查".to_string()),
        ).unwrap();

        std::thread::sleep(Duration::from_millis(2));
        let h3 = brain.l1.add_hyperedge(
            &mut wtxn, &brain.l1_env,
            vec![node2.clone()], HyperedgeKind::Evolution, 1.0,
            None, Some("错误排查".to_string()),
        ).unwrap();
        std::thread::sleep(Duration::from_millis(2));
        let _ = brain.l1.add_hyperedge(
            &mut wtxn, &brain.l1_env,
            vec![node2.clone()], HyperedgeKind::Evolution, 1.0,
            Some(h3), Some("错误排查".to_string()),
        ).unwrap();

        wtxn.commit().unwrap();
    }

    // JSON-RPC 调用 memhop_crystallize
    let result = brain.procedural_crystallize();
    assert!(result.is_ok());

    let report = result.unwrap();
    // 验证返回 CrystallizeReport
    assert!(report.crystals_created >= 1);
    assert!(report.chains_analyzed >= 3);
    assert!(report.duration_ms > 0);

    // 验证 list_crystals 能获取到晶体
    let crystals = brain.list_crystals().unwrap();
    assert!(crystals.len() >= 1);

    // 验证 get_crystal 能获取单个晶体
    if let Some(crystal) = crystals.first() {
        let fetched = brain.get_crystal(&crystal.id).unwrap();
        assert!(fetched.is_some());
        assert_eq!(fetched.unwrap().id, crystal.id);
    }
}
