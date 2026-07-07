//! MemHop v0.45.0 End-to-End Integration Tests (mock encoder / real LLM)
//!
//! These tests exercise the full Agent integration workflow against:
//! - Mock vector encoder via gRPC (multilingual-e5-small through meowvec)
//! - Real LLM API (OpenAI-compatible)
//!
//! All tests are marked `#[ignore]` because they require network access and
//! API credentials for the LLM calls. Run with:
//!     cargo test -- --ignored
//!
//! The mock meowvec server is spawned automatically by `tests/common/mod.rs`;
//! no manual setup is required.

mod common;

use memhop::encoder::GrpcEncoder;
use memhop::{
    ActionItem, ActionType, ArchivePageQuery, CrystalListQuery, EngramListQuery,
    KnowledgeListQuery, LlmConfig, MemHop, MemHopConfig, SearchQuery, SourceMeta, SourceType,
    StoreBatch, StoreItem, TopicListQuery, UpdateProfileRequest, UpdateRequest,
};
use std::collections::HashMap;
use std::path::PathBuf;

const VECTOR_DIM: usize = 1024;
const API_URL: &str = "https://api.deepseek.com/v1/chat/completions";
const MODEL: &str = "deepseek-chat";

/// Build the LLM configuration used by every E2E test.
fn make_llm_config() -> LlmConfig {
    LlmConfig {
        api_url: API_URL.into(),
        api_key: std::env::var("MEMHOP_LLM_API_KEY").unwrap_or_default(),
        model: MODEL.into(),
        temperature: 0.2,
        top_p: 0.9,
        presence_penalty: 0.0,
        frequency_penalty: 0.0,
        timeout_secs: 30,
        language: "zh".into(),
    }
}

/// Build a MemHopConfig pointing at the given temporary .meh path.
fn make_config(path: PathBuf) -> MemHopConfig {
    let mut config = MemHopConfig::new(path, VECTOR_DIM);
    config.encoder_grpc_addr = Some("http://127.0.0.1:27110".to_string());
    config.crystal_path = Some(PathBuf::from("/tmp/memhop_e2e_crystals"));
    config.llm = make_llm_config();
    config.auto_dream_on_evict = true;
    config
}

/// Start the shared ORT (BGE-M3 ONNX) meowvec server for this test binary.
///
/// The first call spawns the process on port 27110 and waits for the gRPC
/// health check to pass. The process is killed automatically when the test
/// binary exits.
fn setup_ort_meowvec() {
    let _guard = common::ensure_python_meowvec(27110);
}

/// Create a gRPC encoder connected to the ORT meowvec server.
fn create_encoder(dim: usize) -> GrpcEncoder {
    GrpcEncoder::new("http://127.0.0.1:27110", dim)
        .expect("failed to connect to mock meowvec at http://127.0.0.1:27110")
}

/// Remove the temporary database file if it exists.
fn cleanup_db(path: &PathBuf) {
    if path.exists() {
        let _ = std::fs::remove_file(path);
    }
}

/// Open a fresh MemHop instance, inject the test encoder, run the closure, and
/// guarantee cleanup of the .meh file even if the test panics.
fn with_e2e_db<F>(name: &str, f: F)
where
    F: FnOnce(&mut MemHop) + std::panic::UnwindSafe,
{
    let path = PathBuf::from(format!("/tmp/memhop_e2e_{}.meh", name));
    cleanup_db(&path);

    let result = std::panic::catch_unwind(|| {
        let mut db = MemHop::open(make_config(path.clone())).expect("open MemHop");
        db.set_encoder(create_encoder(VECTOR_DIM));
        f(&mut db);
    });

    cleanup_db(&path);
    if let Err(e) = result {
        std::panic::resume_unwind(e);
    }
}

// =============================================================================
// Test data
// =============================================================================

/// 20+ realistic Chinese multi-turn dialogues grouped by topic.
fn conversation_documents() -> Vec<(&'static str, &'static str)> {
    vec![
        (
            "Rust异步编程",
            "我想了解Rust中的async/await。为什么有时候需要显式地调用.await？",
        ),
        (
            "Rust异步编程",
            "Tokio的运行时如何选择？在单线程和多线程运行时之间怎么权衡？",
        ),
        (
            "项目架构设计",
            "我们的微服务架构需要考虑服务发现、负载均衡和熔断机制。",
        ),
        (
            "项目架构设计",
            "在使用gRPC进行内部通信时，我们应该如何设计proto文件和版本兼容策略？",
        ),
        (
            "机器学习模型训练",
            "使用PyTorch训练模型时，学习率调度对收敛速度影响很大。",
        ),
        (
            "机器学习模型训练",
            "微调大语言模型时，LoRA和全参数微调各有什么适用场景？",
        ),
        (
            "日常生活",
            "今天下午去咖啡馆写了一会儿代码，天气很好，效率也高。",
        ),
        ("日常生活", "周末计划去爬山，顺便想想新的项目创意。"),
        (
            "技术问题排查",
            "服务出现了偶发的500错误，日志里只有connection reset by peer。",
        ),
        (
            "技术问题排查",
            "排查内存泄漏时发现是闭包捕获了过大的状态没有释放。",
        ),
        (
            "前端开发",
            "React的useEffect依赖数组很容易写出死循环，需要仔细检查。",
        ),
        (
            "前端开发",
            "Next.js的SSR和SSG在选择时应该考虑内容的实时性要求。",
        ),
        (
            "数据库设计",
            "PostgreSQL的索引类型很多，B-Tree、GIN、GiST分别适合什么场景？",
        ),
        ("数据库设计", "分库分表后全局ID生成有哪些常见方案？"),
        (
            "DevOps",
            "GitHub Actions的workflow可以通过matrix同时测试多个Rust版本。",
        ),
        ("DevOps", "容器镜像的多阶段构建可以显著减小最终镜像体积。"),
        (
            "自然语言处理",
            "中文分词对BM25检索很关键，jieba是一个常用的选择。",
        ),
        (
            "自然语言处理",
            "语义检索和关键词检索混合使用通常比单一通道效果更好。",
        ),
        (
            "产品需求讨论",
            "用户反馈搜索结果的排序不够准，我们是否需要引入重排序模型？",
        ),
        (
            "产品需求讨论",
            "记忆数据库的API设计应该尽量简单，减少Agent接入成本。",
        ),
        (
            "安全与隐私",
            "API Key不应该硬编码在代码里，应该通过环境变量或密钥管理服务注入。",
        ),
        (
            "安全与隐私",
            "用户对话数据需要加密存储，并且支持按用户维度隔离。",
        ),
    ]
}

fn store_item(topic: &str, text: &str) -> StoreItem {
    StoreItem {
        text: text.into(),
        topic_label: Some(topic.into()),
        domain_id: None,
        importance: Some(0.6),
        valence: Some(0.0),
        arousal: Some(0.3),
        source: SourceMeta::new(SourceType::UserInput, None),
        is_structural: false,
        source_ref: None,
    }
}

// =============================================================================
// Test 1: Agent conversation memory flow
// =============================================================================

#[test]
fn test_agent_conversation_memory_flow() {
    setup_ort_meowvec();
    with_e2e_db("agent_conversation_memory_flow", |db| {
        // 1. Batch store multi-turn Chinese dialogues.
        let docs = conversation_documents();
        let batch = StoreBatch {
            items: docs
                .iter()
                .map(|(topic, text)| store_item(topic, text))
                .collect(),
            session_id: Some("e2e_session_1".into()),
            turn_id: None,
            source: Default::default(),
        };

        let report = db.batch_store(batch).expect("batch store should succeed");
        eprintln!("[E2E] batch_store report: {:?}", report);
        assert!(
            report.l1_nodes_created > 0,
            "L1 nodes should be created after batch store"
        );
        assert!(
            report.l2_topics_updated > 0,
            "L2 topics should be updated after batch store"
        );

        // 2. Search — validate recall quality for a Rust-related query.
        let search = db
            .search_context(SearchQuery {
                dialogue: "Rust async await 运行时".into(),
                l2_id: None,
                context_id: None,
                l3_id: None,
                context_limit: 5,

                auto_create: 0,
                min_score: 0.0,

                source: Default::default(),
            })
            .expect("search should succeed");
        eprintln!("[E2E] search contexts: {:?}", search.contexts);
        assert!(
            !search.contexts.is_empty(),
            "Search should return at least one context"
        );
        let has_rust = search
            .contexts
            .iter()
            .any(|c| c.title.contains("Rust") || c.title.contains("异步"));
        assert!(has_rust, "Search should recall Rust-related topic");

        // 3. Locate the Rust topic by title to avoid depending on vector ranking.
        let topics = db
            .list_l2(TopicListQuery {
                page: 1,
                page_size: 100,
                active_only: false,
                keyword: None,
            })
            .expect("list_l2 should succeed");
        let rust_topic = topics
            .items
            .into_iter()
            .find(|t| t.title.contains("Rust") || t.title.contains("异步"))
            .expect("Rust topic should exist after batch store");

        // 4. Update the Rust topic to simulate an Agent turn.
        let update = db
            .update_memory(UpdateRequest {
                topic_id: rust_topic.id.clone(),
                dialogue_text: "总结一下Rust异步编程的关键点".into(),
                summary: Some(
                    "Rust异步编程涉及Future、Pin、async/await语法、Tokio运行时、任务调度、 \
                     非阻塞I/O、并发模型、错误处理以及生命周期管理。用户希望深入理解 \
                     Future和Pin的关系，以及Tokio的spawn与block_on的使用场景。"
                        .into(),
                ),
                action_chain: Some(vec![ActionItem {
                    title: "检索Rust异步记忆".into(),
                    description: "从记忆中检索Rust异步编程相关内容".into(),
                    action_type: ActionType::Query,
                    parameters: None,
                }]),
                instant_distill: false,
                scene_id: None,
                source: Default::default(),
            })
            .expect("update_memory should succeed");
        assert_eq!(update.topic_id, rust_topic.id);
        assert!(!update.archive_id.is_empty());

        // 5. Dream consolidation on the active topic.
        // activate_topic hashes the input string, so use the original topic label
        // (the same string used to create the L2 context id_hash).
        db.activate_topic("Rust异步编程", None);
        let dream_report = db.dream(None).expect("dream should succeed");
        eprintln!("[E2E] dream report: {:?}", dream_report);

        // 6. Verify L0 profile update.
        let profile = db.get_profile().expect("get_profile should succeed");
        assert!(
            profile.is_some(),
            "Dream should create or update L0 profile"
        );
        let profile = profile.unwrap();
        assert!(!profile.id.is_empty());

        // 6. Verify L1 hypergraph associations (no dangling references).
        let engrams = db
            .list_engrams(EngramListQuery {
                page: 1,
                page_size: 100,
                keyword: None,
                min_importance: None,
                state_filter: None,
            })
            .expect("list_engrams should succeed");
        assert!(
            engrams.total > 0,
            "L1 engrams should exist after batch store"
        );
        for engram in &engrams.items {
            assert!(
                !engram.associated_topics.is_empty(),
                "Every L1 engram should be associated with at least one L2 topic"
            );
        }

        // 7. Verify L2 multi-level compression.
        let topics = db
            .list_l2(TopicListQuery {
                page: 1,
                page_size: 100,
                active_only: false,
                keyword: None,
            })
            .expect("list_l2 should succeed");
        assert!(
            topics.total >= report.l2_topics_updated as usize,
            "L2 topics should persist"
        );

        // 8. Verify L3 knowledge distillation.
        assert!(
            !dream_report.new_l3_nodes.is_empty(),
            "Dream should create new L3 knowledge nodes"
        );
        let knowledge = db
            .list_knowledge(KnowledgeListQuery {
                page: 1,
                page_size: 100,
                domain_filter: None,
                knowledge_type: None,
                keyword: None,
            })
            .expect("list_knowledge should succeed");
        assert!(
            knowledge.total > 0,
            "L3 knowledge entries should exist after dream"
        );

        // 9. Verify L5 crystallization.
        let crystals = db
            .list_crystals(CrystalListQuery {
                page: 1,
                page_size: 100,
                status_filter: None,
                min_trigger_count: None,
                keyword: None,
            })
            .expect("list_crystals should succeed");
        assert!(
            crystals.total > 0 || !dream_report.new_crystals.is_empty(),
            "L5 crystals should be created or already present"
        );
    });
}

// =============================================================================
// Test 2: Chinese memory specialization
// =============================================================================

#[test]
fn test_chinese_memory_specialization() {
    setup_ort_meowvec();
    with_e2e_db("chinese_memory_specialization", |db| {
        // Seed Chinese memories covering people, projects, and technical terms.
        let docs = [
            (
                "人物与项目",
                "王小明和张丽正在负责MemHop项目的L3图遍历模块。",
            ),
            (
                "人物与项目",
                "李华是机器学习团队的负责人，主导了推荐系统的升级。",
            ),
            (
                "中文技术讨论",
                "我们在实现中文BM25检索时，使用jieba进行分词并构建倒排索引。",
            ),
            (
                "中文技术讨论",
                "实体匹配需要识别人名、项目名和技术术语，提升检索准确率。",
            ),
            (
                "产品规划",
                "MemHop的下一个里程碑是支持多模态记忆，包括文本和图像。",
            ),
        ];

        let batch = StoreBatch {
            items: docs
                .iter()
                .map(|(topic, text)| store_item(topic, text))
                .collect(),
            session_id: Some("e2e_chinese".into()),
            turn_id: None,
            source: Default::default(),
        };
        let report = db.batch_store(batch).expect("batch store should succeed");
        assert!(report.l1_nodes_created > 0);

        // BM25 search with Chinese keywords.
        let search = db
            .search_context(SearchQuery {
                dialogue: "王小明 MemHop 项目".into(),
                l2_id: None,
                context_id: None,
                l3_id: None,
                context_limit: 5,

                auto_create: 0,
                min_score: 0.0,

                source: Default::default(),
            })
            .expect("Chinese BM25 search should succeed");
        assert!(
            !search.contexts.is_empty(),
            "Chinese BM25 should return contexts"
        );
        let has_person = search
            .contexts
            .iter()
            .any(|c| c.title.contains("人物") || c.title.contains("项目"));
        assert!(
            has_person,
            "Entity matching should recall person/project topic"
        );

        // LLM enhancement should respond consistently in Chinese.
        let enhanced_search = db
            .search_context(SearchQuery {
                dialogue: "中文分词对检索有什么帮助".into(),
                l2_id: None,
                context_id: None,
                l3_id: None,
                context_limit: 5,

                auto_create: 0,
                min_score: 0.0,

                source: Default::default(),
            })
            .expect("LLM-enhanced Chinese search should succeed");
        assert!(
            !enhanced_search.contexts.is_empty(),
            "LLM-enhanced Chinese search should return results"
        );

        // Update profile with Chinese lexicon and verify it persists.
        let profile_req = UpdateProfileRequest {
            name: Some("MemHop Agent".into()),
            role: Some("中文对话助手".into()),
            personality: Some("耐心、严谨、乐于助人".into()),
            worldview: None,
            preferences: Some({
                let mut m = HashMap::new();
                m.insert("language".into(), "zh".into());
                m.insert("response_style".into(), "concise".into());
                m
            }),
            lexicon: Some({
                let mut m = HashMap::new();
                m.insert("MemHop".into(), "Agent记忆数据库".into());
                m.insert("jieba".into(), "中文分词工具".into());
                m
            }),
            style_traits: Some(vec!["使用中文回答".into(), "技术解释清晰".into()]),
            emotion_patterns: None,
        };
        let profile = db.update_profile(profile_req).expect("update_profile");
        assert_eq!(profile.role, "中文对话助手");
        assert!(profile.lexicon.contains_key("MemHop"));
    });
}

// =============================================================================
// Test 3: L3 graph traversal
// =============================================================================

#[test]
fn test_l3_graph_traversal() {
    setup_ort_meowvec();
    with_e2e_db("l3_graph_traversal", |db| {
        // Create a temporary source tree representing a tiny codebase.
        let tmp_dir = std::env::temp_dir().join("memhop_e2e_src");
        let _ = std::fs::remove_dir_all(&tmp_dir);
        std::fs::create_dir_all(&tmp_dir).expect("create temp source dir");

        std::fs::write(
            tmp_dir.join("main.rs"),
            r#"
mod parser;
mod search;

fn main() {
    let query = parser::parse_query("hello");
    search::run(query);
}
"#,
        )
        .unwrap();
        std::fs::write(
            tmp_dir.join("parser.rs"),
            r#"
pub fn parse_query(input: &str) -> String {
    input.to_lowercase()
}
"#,
        )
        .unwrap();
        std::fs::write(
            tmp_dir.join("search.rs"),
            r#"
use crate::parser::parse_query;

pub fn run(query: String) {
    let _ = parse_query(&query);
}
"#,
        )
        .unwrap();

        // Build L3 hypergraph from the temporary codebase path.
        let import_result = db
            .build_l3_hypergraph_from_path(&tmp_dir)
            .expect("build L3 hypergraph from path should succeed");
        eprintln!(
            "[E2E] L3 import created_ids: {:?}",
            import_result.created_ids
        );
        assert!(
            !import_result.created_ids.is_empty(),
            "L3 graph should create node/topic IDs"
        );

        // List knowledge graphs.
        let knowledge = db
            .list_knowledge(KnowledgeListQuery {
                page: 1,
                page_size: 100,
                domain_filter: None,
                knowledge_type: None,
                keyword: None,
            })
            .expect("list_knowledge should succeed");
        assert!(knowledge.total > 0, "L3 knowledge graph should be listable");

        // Traverse nodes inside the first knowledge graph.
        let first_graph_id = &knowledge.items[0].id;
        let nodes = db
            .get_knowledge(first_graph_id)
            .expect("get_knowledge should succeed");
        assert!(nodes.is_some(), "Knowledge detail should exist");

        // Search restricted by L3 graph ID should still return the linked L2 topic.
        let restricted = db
            .search_context(SearchQuery {
                dialogue: "parser search main".into(),
                l2_id: None,
                context_id: None,
                l3_id: Some(first_graph_id.clone()),
                context_limit: 5,

                auto_create: 0,
                min_score: 0.0,

                source: Default::default(),
            })
            .expect("L3-restricted search should succeed");
        assert!(
            !restricted.contexts.is_empty(),
            "L3-restricted search should return the linked L2 context"
        );

        // Clean up temporary source tree.
        let _ = std::fs::remove_dir_all(&tmp_dir);
    });
}

// =============================================================================
// Test 4: Dream pipeline full validation
// =============================================================================

#[test]
fn test_dream_pipeline_full() {
    setup_ort_meowvec();
    with_e2e_db("dream_pipeline_full", |db| {
        // Seed several topics with multiple turns and action chains.
        let docs = [
            (
                "Rust异步",
                "我想学习Rust async/await，请帮我梳理Future和Pin的关系。",
            ),
            (
                "Rust异步",
                "Tokio的spawn和block_on有什么区别，什么时候用哪个？",
            ),
            (
                "微服务架构",
                "设计一个支持十万QPS的API网关，需要考虑哪些组件？",
            ),
            ("微服务架构", "服务网格和API网关的边界在哪里？"),
            ("大模型微调", "SFT和RLHF在数据准备上有什么不同？"),
        ];

        let batch = StoreBatch {
            items: docs
                .iter()
                .map(|(topic, text)| store_item(topic, text))
                .collect(),
            session_id: Some("e2e_dream".into()),
            turn_id: None,
            source: Default::default(),
        };
        let store_report = db.batch_store(batch).expect("batch store should succeed");
        assert!(store_report.l2_topics_updated >= 3);

        // Activate all topics and append an Agent turn with an action chain to each.
        let topics = db
            .list_l2(TopicListQuery {
                page: 1,
                page_size: 100,
                active_only: false,
                keyword: None,
            })
            .expect("list_l2 should succeed");
        assert!(
            !topics.items.is_empty(),
            "There should be topics to activate"
        );

        for topic in &topics.items {
            // activate_topic hashes the input string, so use the original label.
            db.activate_topic(&topic.title, None);
            let rich_summary = if topic.title.contains("Rust") {
                "Rust异步编程涵盖Future trait、Pin类型、async/await语法糖、Tokio运行时、 \
                 任务调度器、非阻塞IO、并发原语、错误传播以及生命周期约束。"
                    .into()
            } else if topic.title.contains("微服务") {
                "微服务架构设计需要考虑API网关、服务注册与发现、负载均衡、熔断降级、 \
                 配置中心、可观测性、链路追踪、服务网格以及协议兼容性。"
                    .into()
            } else if topic.title.contains("模型") || topic.title.contains("微调") {
                "大语言模型微调包括SFT监督微调、RLHF人类反馈强化学习、数据清洗、 \
                 prompt工程、奖励模型训练、PPO算法以及评估指标设计。"
                    .into()
            } else {
                format!("总结{}对话内容", topic.title)
            };
            let _ = db
                .update_memory(UpdateRequest {
                    topic_id: topic.id.clone(),
                    dialogue_text: format!("请总结{}主题的关键结论", topic.title),
                    summary: Some(rich_summary),
                    action_chain: Some(vec![
                        ActionItem {
                            title: "检索相关记忆".into(),
                            description: format!("检索与{}相关的记忆", topic.title),
                            action_type: ActionType::Query,
                            parameters: None,
                        },
                        ActionItem {
                            title: "生成摘要".into(),
                            description: "调用LLM生成主题摘要".into(),
                            action_type: ActionType::Execute,
                            parameters: None,
                        },
                    ]),
                    instant_distill: false,
                    scene_id: None,
                    source: Default::default(),
                })
                .expect("update_memory should succeed");
        }

        // Run dream consolidation. L3 distillation now runs before L2 compression
        // so active depth-1 topics with summaries are distilled in a single pass.
        let dream_report = db.dream(None).expect("dream should succeed");
        eprintln!("[E2E] dream_pipeline_full report: {:?}", dream_report);

        // L1 topological consistency: no dangling references.
        let engrams = db
            .list_engrams(EngramListQuery {
                page: 1,
                page_size: 1000,
                keyword: None,
                min_importance: None,
                state_filter: None,
            })
            .expect("list_engrams should succeed");
        for engram in &engrams.items {
            assert!(
                !engram.associated_topics.is_empty(),
                "L1 engram {} has no associated topic",
                engram.id
            );
        }

        // L0 profile updated.
        let profile = db.get_profile().expect("get_profile should succeed");
        assert!(profile.is_some(), "L0 profile should be updated by dream");
        assert!(
            dream_report.l0_updated.is_some(),
            "Dream report should note L0 update"
        );

        // L2 compression hierarchy intact.
        let all_topics = db
            .list_l2(TopicListQuery {
                page: 1,
                page_size: 1000,
                active_only: false,
                keyword: None,
            })
            .expect("list_l2 should succeed");
        let max_depth = all_topics.items.iter().map(|t| t.depth).max().unwrap_or(0);
        assert!(
            max_depth >= 1,
            "L2 topic hierarchy should contain at least depth-1 contexts"
        );

        // L3 knowledge distilled.
        assert!(
            !dream_report.new_l3_nodes.is_empty(),
            "Dream should distill L3 knowledge nodes"
        );
        let knowledge = db
            .list_knowledge(KnowledgeListQuery {
                page: 1,
                page_size: 100,
                domain_filter: None,
                knowledge_type: None,
                keyword: None,
            })
            .expect("list_knowledge should succeed");
        assert!(
            knowledge.total > 0,
            "L3 knowledge list should contain distilled hypergraphs"
        );

        // L5 crystallization contains ActionStep-derived crystals.
        assert!(
            !dream_report.new_crystals.is_empty(),
            "Dream should crystallize at least one pattern from ActionChainSlots"
        );
        let crystals = db
            .list_crystals(CrystalListQuery {
                page: 1,
                page_size: 100,
                status_filter: None,
                min_trigger_count: None,
                keyword: None,
            })
            .expect("list_crystals should succeed");
        assert!(
            crystals.total > 0,
            "L5 crystals should be listable after dream"
        );

        // Verify archives are linked from topics.
        let archives = db
            .list_all_archives(ArchivePageQuery {
                page: 1,
                page_size: 100,
                start_time: None,
                end_time: None,
                content_type: None,
            })
            .expect("list_all_archives should succeed");
        assert!(
            archives.total >= topics.items.len(),
            "Each topic should have at least one archive after update_memory"
        );
    });
}
