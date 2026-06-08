use memhop_core::{
    Brain, BrainConfig, Encoder, NgramEncoder, RecallRequest, StoreBatch, StoreItem, Layer,
};
#[cfg(feature = "candle")]
use memhop_core::CandleEncoder;
use memhop_core::EncoderRouter;
use std::sync::Arc;

#[test]
fn test_vector_model_integration() {
    // 创建编码器：优先使用向量模型
    let encoder: Arc<Box<dyn Encoder>> = {
        #[cfg(feature = "candle")]
        {
            // 使用 CandleEncoder (multilingual-e5-small, 384维)
            let model_path = "/Volumes/zt_hd/projects/meow/memhop/models/multilingual-e5-small";
            match CandleEncoder::new(model_path) {
                Ok(dense_encoder) => {
                    println!("✓ CandleEncoder loaded successfully (384 dim)");
                    // 双编码器模式：NgramEncoder (sparse) + CandleEncoder (dense)
                    let sparse_encoder = Box::new(NgramEncoder::new(384));
                    let router = EncoderRouter::new(sparse_encoder, Box::new(dense_encoder));
                    Arc::new(Box::new(router))
                }
                Err(e) => {
                    println!("⚠ Failed to load CandleEncoder: {}, falling back to NgramEncoder", e);
                    Arc::new(Box::new(NgramEncoder::new(1024)))
                }
            }
        }

        #[cfg(not(feature = "candle"))]
        {
            println!("⚠ candle feature not enabled, using NgramEncoder only");
            Arc::new(Box::new(NgramEncoder::new(1024)))
        }
    };

    // 测试编码器
    let output = encoder.encode("Hello, this is a test query");
    println!("✓ Encoder output: {} dense dims, {} sparse terms",
             output.dense.len(), output.sparse.len());

    // 创建 Brain 实例
    let tmp = tempfile::tempdir().unwrap();
    let cfg = BrainConfig {
        brains_dir: tmp.path().to_str().unwrap().to_string(),
        agent_id: "test".to_string(),
    };
    let brain = Brain::open(cfg, encoder).unwrap();
    println!("✓ Brain created successfully");

    // 测试存储和召回
    let items = vec![
        StoreItem {
            text: "The capital of France is Paris".to_string(),
            source: "test".to_string(),
            turn_id: Some("turn_0".to_string()),
            session_id: Some("session_1".to_string()),
            topic_label: Some("geography".to_string()),
            llm_keywords: Some(vec!["paris".to_string(), "france".to_string()]),
            llm_compressed_summary: Some("Paris is France capital".to_string()),
            valence: Some(0.5),
            arousal: Some(0.3),
            chain_parent_id: None,
            chain_label: Some("conversation".to_string()),
            domain_id: None,
            importance: Some(0.8),
        },
        StoreItem {
            text: "Python is a popular programming language".to_string(),
            source: "test".to_string(),
            turn_id: Some("turn_1".to_string()),
            session_id: Some("session_1".to_string()),
            topic_label: Some("programming".to_string()),
            llm_keywords: Some(vec!["python".to_string(), "programming".to_string()]),
            llm_compressed_summary: Some("Python is popular".to_string()),
            valence: Some(0.7),
            arousal: Some(0.4),
            chain_parent_id: Some("turn_0".to_string()),
            chain_label: Some("conversation".to_string()),
            domain_id: None,
            importance: Some(0.7),
        },
    ];

    let mut brain_mut = brain;
    brain_mut.batch_store(StoreBatch { items }).unwrap();
    println!("✓ Stored 2 items successfully");

    // 测试召回
    let req = RecallRequest {
        query: "What is the capital of France?".to_string(),
        max_results: 5,
        target_layers: vec![Layer::L1, Layer::L2, Layer::L3, Layer::L4],
        ..Default::default()
    };

    let resp = brain_mut.recall(&req).unwrap();
    println!("✓ Recall returned {} results", resp.results.len());

    // 检查是否召回了相关记忆
    let has_paris = resp.results.iter().any(|r| r.text.contains("Paris"));
    println!("✓ Recall found Paris memory: {}", has_paris);

    assert!(has_paris, "Should recall Paris memory");
    assert!(resp.results.len() > 0, "Should have at least one result");
}

#[test]
fn test_longmemeval_synthetic_dataset() {
    use memhop_core::bench_support::dataset_loader::LongMemEvalDataset;

    let dataset = LongMemEvalDataset::synthesize();
    println!("✓ LongMemEval dataset synthesized: {} sessions", dataset.sessions.len());

    // 验证数据集结构
    assert!(dataset.sessions.len() > 0, "Should have at least one session");
    let first_session = &dataset.sessions[0];
    println!("✓ First session: {} turns, {} questions",
             first_session.turns.len(), first_session.questions.len());

    assert!(first_session.turns.len() > 0, "Session should have turns");
    assert!(first_session.questions.len() > 0, "Session should have questions");
}
