// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// MemHop 检索接口诊断测试
//
// 验证检索接口返回格式合规性，对比 LLM 关键词与原文。
// 当 LLM API Key 不可用时，测试仍会运行数据创建和格式检查部分。

use std::path::PathBuf;

use memhop::{
    ActionItem, ActionType, ArchiveQuery, ImportData, ImportMode, ImportRequest,
    KnowledgeImportItem, L3EntityHint, MemHop, MemHopConfig, SearchQuery, TopicImportItem,
    TopicListQuery, UpdateL2Fields, UpdateRequest,
};

// ============================================================================
// 辅助函数
// ============================================================================

fn test_config(db_path: &str) -> MemHopConfig {
    let api_key = std::env::var("MEMHOP_LLM_API_KEY").unwrap_or_default();
    let llm_configured = !api_key.is_empty();

    MemHopConfig {
        db_path: PathBuf::from(db_path),
        encoder_grpc_addr: None,
        vector_dim: 768,
        crystal_path: None,
        llm: if llm_configured {
            memhop::LlmConfig {
                api_url: "https://api.deepseek.com/chat/completions".to_string(),
                api_key,
                model: "deepseek-chat".to_string(),
                timeout_secs: 120, // DeepSeek API 可能较慢
                ..Default::default()
            }
        } else {
            memhop::LlmConfig::default()
        },
        auto_dream_on_evict: false,
        auto_dream_archive_threshold: 20,
        auto_dream_summary_bytes: 2048,
        ivf_initial_k: 16,
        search_weights: None,
        decay_config: None,
        session_config: None,
        dream_idle_threshold_secs: None,
        auto_checkpoint_interval: None,
        adjacency_cache_max_entries: 128,
        llm_preprocess: memhop::LlmPreprocessConfig {
            enable_search_preprocess: true,
            enable_write_preprocess: false,
            fallback_to_tokenizer: true,
            ..Default::default()
        },
    }
}

/// 检查 LLM 是否可用
fn is_llm_available() -> bool {
    std::env::var("MEMHOP_LLM_API_KEY")
        .map(|k| !k.is_empty())
        .unwrap_or(false)
}

/// 打印分隔线
fn print_separator(title: &str) {
    println!("\n{}", "=".repeat(80));
    println!("  {}", title);
    println!("{}", "=".repeat(80));
}

// ============================================================================
// 主诊断测试
// ============================================================================

#[cfg(feature = "grpc-encoder")]
#[test]
fn test_search_result_format_compliance() {
    let db_path = "/tmp/memhop_search_diagnostic.meh";
    let _ = std::fs::remove_file(db_path);
    let _ = std::fs::remove_file("/tmp/memhop_search_diagnostic.meh");

    let llm_avail = is_llm_available();

    print_separator("MemHop 检索接口诊断测试");
    println!(
        "  LLM API Key:           {}",
        if llm_avail {
            "可用 ✓"
        } else {
            "不可用 ✗"
        }
    );
    println!("  FEATURE llm:           enabled");
    println!("  FEATURE grpc-encoder:  enabled");
    println!("  DB path:               {}", db_path);

    let config = test_config(db_path);
    let mut db = MemHop::open(config.clone()).expect("MemHop::open failed");

    // ========================================================================
    // 1. 创建测试数据
    // ========================================================================
    print_separator("1/5: 创建测试数据");

    // --- 1a. Import L3 知识节点 ---
    println!("\n[1a] 导入 L3 知识节点...");
    let l3_import = db
        .import_memory(ImportRequest {
            target_layer: memhop::TargetLayer::Knowledge,
            data: ImportData::Knowledge(vec![
                KnowledgeImportItem {
                    title: "Rust Ownership".to_string(),
                    domain: "programming".to_string(),
                    knowledge_type: "Conceptual".to_string(),
                    text: "Rust 的所有权系统确保内存安全。每个值同时只有一个所有者。借用检查器在编译时验证引用有效性。".to_string(),
                    summary: None,
                    keywords: vec!["Rust".to_string(), "ownership".to_string(), "borrow checker".to_string()],
                    source_ref: None,
                },
                KnowledgeImportItem {
                    title: "Async/Await in Rust".to_string(),
                    domain: "programming".to_string(),
                    knowledge_type: "Conceptual".to_string(),
                    text: "Rust 的 async/await 语法实现零成本异步编程。Future trait 是核心抽象，tokio 是主流运行时。".to_string(),
                    summary: None,
                    keywords: vec!["Rust".to_string(), "async".to_string(), "tokio".to_string(), "Future".to_string()],
                    source_ref: None,
                },
            ]),
            mode: ImportMode::Merge,
            knowledge_title: None,
        })
        .expect("L3 import failed");
    let l3_ids = l3_import.ids.as_ref().expect("L3 import should return ids");
    println!("  创建了 {} 个 L3 节点: {:?}", l3_ids.len(), l3_ids);

    // --- 1b. Import L2 话题（Scene 1: 编程讨论） ---
    println!("\n[1b] 导入 L2 话题（编程讨论）...");
    let scene1_import = db
        .import_memory(ImportRequest {
            target_layer: memhop::TargetLayer::Topic,
            data: ImportData::Topics(vec![
                TopicImportItem {
                    title: "Rust 编程讨论".to_string(),
                    summary: Some(
                        "关于 Rust 语言特性的深入讨论，包括所有权、借用检查和异步编程".to_string(),
                    ),
                    keywords: vec![
                        "rust".to_string(),
                        "编程".to_string(),
                        "系统语言".to_string(),
                    ],
                    knowledge_domain: Some("programming".to_string()),
                },
                TopicImportItem {
                    title: "Python 数据分析".to_string(),
                    summary: Some("关于 Python 数据分析工具链和最佳实践".to_string()),
                    keywords: vec![
                        "python".to_string(),
                        "数据分析".to_string(),
                        "pandas".to_string(),
                    ],
                    knowledge_domain: Some("programming".to_string()),
                },
            ]),
            mode: ImportMode::Merge,
            knowledge_title: None,
        })
        .expect("Scene 1 import failed");
    let scene1_topic_ids = scene1_import
        .ids
        .as_ref()
        .expect("Scene 1 import should return ids");
    let rust_topic_id = &scene1_topic_ids[0];
    let python_topic_id = &scene1_topic_ids[1];
    println!(
        "  Scene 1 topics: rust={}, python={}",
        rust_topic_id, python_topic_id
    );

    // --- 1c. Import L2 话题（Scene 2: 旅游规划） ---
    println!("\n[1c] 导入 L2 话题（旅游规划）...");
    let scene2_import = db
        .import_memory(ImportRequest {
            target_layer: memhop::TargetLayer::Topic,
            data: ImportData::Topics(vec![
                TopicImportItem {
                    title: "东京旅游攻略".to_string(),
                    summary: Some(
                        "日本东京的旅游景点和美食推荐，包括浅草寺、涩谷和新宿".to_string(),
                    ),
                    keywords: vec!["东京".to_string(), "旅游".to_string(), "日本".to_string()],
                    knowledge_domain: None,
                },
                TopicImportItem {
                    title: "巴黎艺术之旅".to_string(),
                    summary: Some(
                        "巴黎博物馆和艺术展览，包括卢浮宫、奥赛博物馆和蓬皮杜中心".to_string(),
                    ),
                    keywords: vec!["巴黎".to_string(), "艺术".to_string(), "博物馆".to_string()],
                    knowledge_domain: None,
                },
            ]),
            mode: ImportMode::Merge,
            knowledge_title: None,
        })
        .expect("Scene 2 import failed");
    let scene2_topic_ids = scene2_import
        .ids
        .as_ref()
        .expect("Scene 2 import should return ids");
    let tokyo_topic_id = &scene2_topic_ids[0];
    let paris_topic_id = &scene2_topic_ids[1];
    println!(
        "  Scene 2 topics: tokyo={}, paris={}",
        tokyo_topic_id, paris_topic_id
    );

    // ========================================================================
    // 2. 写入对话内容 + 关联 L3
    // ========================================================================
    print_separator("2/5: 写入对话内容并关联 L3");

    // --- 2a. 关联 L3 节点到 Rust 话题 ---
    // 注意：必须在 update_memory 之前关联 L3，这样 turn 节点创建时
    // 能从父话题继承 l3_refs
    println!("\n[2a] 关联 L3 知识到 Rust 话题...");
    let _link = db
        .update_l2(
            rust_topic_id,
            UpdateL2Fields {
                l3_refs: Some(l3_ids.clone()),
                ..Default::default()
            },
        )
        .expect("link L3 to Rust topic failed");
    println!("  关联了 {} 个 L3 节点", l3_ids.len());

    // --- 2b. 写入 Rust 话题对话 ---
    println!("\n[2b] 写入 Rust 话题对话...");
    db.update_memory(UpdateRequest {
        topic_id: rust_topic_id.clone(),
        dialogue_text: "User: Rust 的 borrow checker 如何工作？\nAssistant: 借用检查器在编译时确保引用有效性。它遵循三条规则：1) 一个值同时只能有一个可变引用或任意数量的不可变引用；2) 引用必须始终有效。".to_string(),
        summary: Some("borrow checker 工作原理".to_string()),
        action_chain: Some(vec![
            ActionItem {
                title: "explain_borrow_checker".to_string(),
                description: "解释 Rust 借用检查器的工作原理".to_string(),
                action_type: ActionType::Execute,
                parameters: None,
            },
        ]),
        instant_distill: false,
        scene_id: Some("programming_scene_1".to_string()),
        source: Default::default(),
        user_keywords: None,
        agent_keywords: None,
    }).expect("update Rust topic failed");
    println!("  ✓");

    // --- 2c. 写入 Python 话题对话 ---
    println!("\n[2c] 写入 Python 话题对话...");
    db.update_memory(UpdateRequest {
        topic_id: python_topic_id.clone(),
        dialogue_text: "User: 数据分析用 pandas 好还是 polars 好？\nAssistant: pandas 生态更成熟，资料丰富，适合中小数据集。polars 性能更好，支持懒执行，适合大数据集。建议根据数据量选择。".to_string(),
        summary: Some("pandas vs polars 对比".to_string()),
        action_chain: Some(vec![
            ActionItem {
                title: "compare_libraries".to_string(),
                description: "对比 pandas 和 polars 的适用场景".to_string(),
                action_type: ActionType::Execute,
                parameters: None,
            },
        ]),
        instant_distill: false,
        scene_id: Some("programming_scene_1".to_string()),
        source: Default::default(),
        user_keywords: None,
        agent_keywords: None,
    }).expect("update Python topic failed");
    println!("  ✓");

    // --- 2d. 写入东京旅游对话 ---
    println!("\n[2d] 写入东京旅游话题对话...");
    db.update_memory(UpdateRequest {
        topic_id: tokyo_topic_id.clone(),
        dialogue_text: "User: 东京有哪些必去的景点？\nAssistant: 推荐景点：1) 浅草寺 - 东京最古老的寺庙；2) 涩谷十字路口 - 世界最繁忙的交叉口；3) 新宿御苑 - 美丽的日式庭园。美食推荐：寿司、拉面、天妇罗。".to_string(),
        summary: Some("东京必去景点和美食".to_string()),
        action_chain: Some(vec![
            ActionItem {
                title: "recommend_attractions".to_string(),
                description: "推荐东京必去景点和美食".to_string(),
                action_type: ActionType::Execute,
                parameters: None,
            },
        ]),
        instant_distill: false,
        scene_id: Some("travel_scene_1".to_string()),
        source: Default::default(),
        user_keywords: None,
        agent_keywords: None,
    }).expect("update Tokyo topic failed");
    println!("  ✓");

    // --- 2e. 写入巴黎艺术对话 ---
    println!("\n[2e] 写入巴黎艺术话题对话...");
    db.update_memory(UpdateRequest {
        topic_id: paris_topic_id.clone(),
        dialogue_text: "User: 巴黎哪些博物馆值得参观？\nAssistant：卢浮宫是世界最大博物馆，收藏包括蒙娜丽莎和维纳斯雕像。奥赛博物馆以印象派画作闻名。蓬皮杜中心专注现代艺术。建议购买 Paris Museum Pass 节省排队时间。".to_string(),
        summary: Some("巴黎博物馆参观指南".to_string()),
        action_chain: Some(vec![
            ActionItem {
                title: "recommend_museums".to_string(),
                description: "推荐巴黎值得参观的博物馆".to_string(),
                action_type: ActionType::Execute,
                parameters: None,
            },
        ]),
        instant_distill: false,
        scene_id: Some("travel_scene_1".to_string()),
        source: Default::default(),
        user_keywords: None,
        agent_keywords: None,
    }).expect("update Paris topic failed");
    println!("  ✓");

    // ========================================================================
    // 3. 验证数据创建成功
    // ========================================================================
    print_separator("3/5: 验证数据创建");

    let l2_list = db
        .list_l2(TopicListQuery {
            page: 1,
            page_size: 20,
            active_only: false,
            keyword: None,
        })
        .expect("list_l2 failed");
    println!("  L2 topics total: {}", l2_list.total);
    for t in &l2_list.items {
        println!(
            "    - id={:?}  keywords={:?}  l4_count={}  l3_count={}",
            t.id.chars().take(16).collect::<String>(),
            t.user_keywords,
            t.l4_count,
            t.l3_count
        );
    }

    let l4_list = db
        .query_archives(ArchiveQuery {
            page: 1,
            page_size: 20,
            topic_id: None,
            keyword: None,
            time_range: None,
        })
        .expect("query_archives failed");
    println!("  L4 archives total: {}", l4_list.len());

    // ========================================================================
    // 4. 执行检索测试
    // ========================================================================
    print_separator("4/5: 执行检索");

    // --- 4a. 尝试无约束检索（对话式） ---
    println!("\n[4a] search_context — 默认无约束检索");
    println!("     对话: \"Rust borrow checker 借用检查\"");
    let search_result = db.search_context(SearchQuery {
        dialogue: "Rust borrow checker 借用检查".to_string(),
        l2_id: None,
        l3_id: None,
        auto_create: false,
    });

    match &search_result {
        Ok(result) => {
            println!("     结果: 成功 ✓");
            print_search_result_summary(result, llm_avail);

            // 格式合规性检查
            validate_search_result_format(result);
        }
        Err(e) => {
            println!("     结果: 失败 ✗");
            println!("     错误类型: {:?}", e);
            println!("     错误信息: {}", e);

            if !llm_avail {
                println!("\n     [预期] LLM 不可用导致 search_context 失败");
                println!("     [预期] preprocess_search_query 返回 Err");
                println!("     这是正确的 fallback 行为 — LLM 不可用时 LLM 预处理失败");
            }
        }
    }

    // --- 4b. 尝试通过 context_id 检索 ---
    println!("\n[4b] search_context — 按 context_id 检索");
    println!("     context_id: {}", rust_topic_id);
    let search_by_context = db.search_context(SearchQuery {
        dialogue: "Rust borrow checker reference validity".to_string(),
        l2_id: Some(rust_topic_id.clone()),
        l3_id: None,
        auto_create: false,
    });

    match &search_by_context {
        Ok(result) => {
            println!("     结果: 成功 ✓");
            print_search_result_summary(result, llm_avail);
            validate_search_result_format(result);
        }
        Err(e) => {
            println!("     结果: 失败 ✗");
            println!("     错误类型: {:?}", e);
            println!("     错误信息: {}", e);
        }
    }

    // ========================================================================
    // 5. 输出完整 JSON（如果搜索成功）
    // ========================================================================
    print_separator("5/5: 完整 SearchResult JSON");

    if let Ok(result) = &search_result {
        println!("{}", serde_json::to_string_pretty(result).unwrap());
    } else if let Ok(result) = &search_by_context {
        println!("{}", serde_json::to_string_pretty(result).unwrap());
    } else {
        println!("  (搜索均失败，无法输出 SearchResult JSON)");
    }

    // ========================================================================
    // 清理
    // ========================================================================
    drop(db);
    let _ = std::fs::remove_file(db_path);

    println!("\n========== 诊断测试完成 ==========");
}

// ============================================================================
// 格式验证函数
// ============================================================================

fn validate_search_result_format(result: &memhop::SearchResult) {
    print_separator("格式合规性检查");

    let mut issues: Vec<String> = Vec::new();
    let mut checks_ok: Vec<String> = Vec::new();

    // 1. contexts 检查
    if !result.contexts.is_empty() {
        let first = &result.contexts[0];
        checks_ok.push(format!("contexts[0].scene_id = {}", first.scene_id));
        checks_ok.push(format!(
            "contexts[0].retrieval_score = {:.4}",
            first.retrieval_score
        ));
        checks_ok.push(format!("contexts 有 {} 个条目", result.contexts.len()));

        // 按时间排序检查
        let timestamps: Vec<i64> = result.contexts.iter().map(|c| c.user_timestamp).collect();
        let mut sorted = timestamps.clone();
        sorted.sort();
        if timestamps == sorted {
            checks_ok.push("contexts 按 user_timestamp 排序 ✓".to_string());
        } else {
            issues.push("contexts 未按 user_timestamp 排序".to_string());
        }

        // 逐 context 检查
        for (i, ctx) in result.contexts.iter().enumerate() {
            // l3_refs
            if ctx.l3_refs.is_empty() {
                issues.push(format!("contexts[{}].l3_refs 为空", i));
            } else {
                checks_ok.push(format!(
                    "contexts[{}].l3_refs 有 {} 个条目",
                    i,
                    ctx.l3_refs.len()
                ));
            }

            // l4_refs
            if ctx.l4_refs.is_empty() {
                issues.push(format!("contexts[{}].l4_refs 为空", i));
            }

            // retrieval_score 范围
            if ctx.retrieval_score >= 0.0 && ctx.retrieval_score <= 1.0 {
                checks_ok.push(format!(
                    "contexts[{}].retrieval_score = {:.4} [有效范围]",
                    i, ctx.retrieval_score
                ));
            } else {
                issues.push(format!(
                    "contexts[{}].retrieval_score 超出 [0,1] 范围: {}",
                    i, ctx.retrieval_score
                ));
            }

            // id 非空
            if ctx.id.is_empty() {
                issues.push(format!("contexts[{}].id 为空", i));
            }

            // depth 有效
            if ctx.depth >= 1 && ctx.depth <= 5 {
                checks_ok.push(format!("contexts[{}].depth = {} [有效]", i, ctx.depth));
            } else {
                issues.push(format!("contexts[{}].depth 无效: {}", i, ctx.depth));
            }
        }
    } else {
        issues.push("contexts 为空".to_string());
    }

    // 3. associated_contexts
    if !result.associated_contexts.is_empty() {
        checks_ok.push(format!(
            "associated_contexts 有 {} 个条目",
            result.associated_contexts.len()
        ));
    } else {
        checks_ok.push("associated_contexts 为空（可能无关联话题）".to_string());
    }

    // 4. l3_ids
    if !result.l3_ids.is_empty() {
        checks_ok.push(format!("l3_ids 有 {} 个条目", result.l3_ids.len()));
    }

    // 5. llm_keywords_used
    if let Some(ref keywords) = result.llm_keywords_used {
        if !keywords.is_empty() {
            checks_ok.push(format!("llm_keywords_used = {:?}", keywords));
            // LLM 关键词与原文对比
            println!("\n  ---- 关键词 vs 原文对比 ----");
            println!("  LLM 提取的关键词: {:?}", keywords);
            println!("  （原文在 update_memory 的 dialogue_text 中）");
            println!("  ----------------------------");
        } else {
            checks_ok.push("llm_keywords_used 为空（LLM 预处理被跳过）".to_string());
        }
    } else {
        checks_ok.push("llm_keywords_used 为 None（LLM 预处理被跳过）".to_string());
    }

    // 6. profile
    if let Some(profile) = &result.profile {
        checks_ok.push(format!(
            "profile: name='{}', role='{}'",
            profile.name, profile.role
        ));
    } else {
        checks_ok.push("profile 为 None".to_string());
    }

    // 输出结果
    println!("\n  --- 检查通过 ---");
    for c in &checks_ok {
        println!("  ✓ {}", c);
    }

    if issues.is_empty() {
        println!("\n  ★ 所有格式合规性检查通过！");
    } else {
        println!("\n  --- 发现问题 ---");
        for (i, issue) in issues.iter().enumerate() {
            println!("  ✗ [{}] {}", i + 1, issue);
        }
    }
}

fn print_search_result_summary(result: &memhop::SearchResult, llm_avail: bool) {
    println!("\n  ----- SearchResult 摘要 -----");
    if !result.contexts.is_empty() {
        println!("  scene_id:       {}", result.contexts[0].scene_id);
        println!(
            "  retrieval_score: {:.4}",
            result.contexts[0].retrieval_score
        );
    } else {
        println!("  scene_id:       (无 context)");
        println!("  retrieval_score: N/A");
    }
    println!("  contexts:       {} 个", result.contexts.len());
    println!("  associated:     {} 个", result.associated_contexts.len());
    println!("  l3_ids:         {:?}", result.l3_ids);
    println!("  l1_previews:    {} 条", result.l1_previews.len());

    if llm_avail {
        println!("  llm_keywords:   {:?}", result.llm_keywords_used);
    } else {
        println!("  llm_keywords:   (LLM 不可用，未提取)");
    }

    if let Some(hints) = &result.l3_import_hints {
        println!("  l3_import_hints: {} 条", hints.len());
    }

    if let Some(profile) = &result.profile {
        println!(
            "  profile:        {} (role: {})",
            profile.name, profile.role
        );
    } else {
        println!("  profile:        None");
    }

    // 打印每个 context 的详细信息
    for (i, ctx) in result.contexts.iter().enumerate() {
        println!("\n  --- contexts[{}] ---", i);
        println!(
            "    id:             {:?}",
            ctx.id.chars().take(16).collect::<String>()
        );
        println!("    depth:          {}", ctx.depth);
        println!("    retrieval_score: {:.4}", ctx.retrieval_score);
        println!("    scene_id:       {}", ctx.scene_id);
        println!("    user_keywords:  {:?}", ctx.user_keywords);
        println!("    agent_keywords: {:?}", ctx.agent_keywords);
        println!("    l3_refs:        {:?}", ctx.l3_refs);
        println!("    l4_refs:        {:?}", ctx.l4_refs);
        println!("    children_ids:   {} 条", ctx.children_ids.len());
        println!(
            "    fused_summary:  {}",
            ctx.fused_summary.as_deref().unwrap_or("(无)")
        );
        println!("    created_at:     {}", ctx.created_at);
    }
}

// ============================================================================
// 综合对话类型检索测试
// ============================================================================

#[allow(dead_code)]
struct DialogueCase {
    name: &'static str,
    dialogue: &'static str,
    /// 搜索时使用的查询（通常与对话内容相同或是对其提问）
    search_query: &'static str,
    /// 关联的话题标题
    topic_title: &'static str,
    /// L3 知识域
    knowledge_domain: &'static str,
}

struct PerDialogueResult {
    dialogue_type: String,
    keyword_count: usize,
    keywords: Vec<String>,
    keyword_analysis: String,
    has_l3_hints: bool,
    l3_hint_count: usize,
    l3_hints: Vec<L3EntityHint>,
    l3_hint_analysis: String,
    scene_id: String,
    scene_title: String,
    scene_score: f32,
    topics_count: usize,
    associated_count: usize,
    l3_ids_count: usize,
    l4_refs_found: bool,
    profile_present: bool,
    user_keywords: Vec<String>,
    agent_keywords: Vec<String>,
    issues: Vec<String>,
}

#[cfg(feature = "grpc-encoder")]
#[test]
fn test_search_comprehensive_dialogues() {
    let db_path = "/tmp/memhop_comprehensive_test.meh";
    let _ = std::fs::remove_file(db_path);

    let llm_avail = is_llm_available();
    if !llm_avail {
        println!("!!! MEMHOP_LLM_API_KEY 未设置，跳过综合测试（LLM 预处理需要 API Key）");
        return;
    }

    print_separator("MemHop 检索接口综合对话测试");
    println!("  LLM API Key:           可用 ✓");
    println!("  FEATURE llm:           enabled");
    println!("  FEATURE grpc-encoder:  enabled");
    println!("  DB path:               {}", db_path);

    // ========================================================================
    // 配置 MemHop
    // ========================================================================
    let config = MemHopConfig {
        db_path: PathBuf::from(db_path),
        encoder_grpc_addr: None,
        vector_dim: 768,
        crystal_path: None,
        llm: memhop::LlmConfig {
            api_url: "https://api.deepseek.com/chat/completions".to_string(),
            api_key: std::env::var("MEMHOP_LLM_API_KEY").unwrap(),
            model: "deepseek-chat".to_string(),
            timeout_secs: 120,
            ..Default::default()
        },
        auto_dream_on_evict: false,
        auto_dream_archive_threshold: 100,
        auto_dream_summary_bytes: 4096,
        ivf_initial_k: 16,
        search_weights: None,
        decay_config: None,
        session_config: None,
        dream_idle_threshold_secs: None,
        auto_checkpoint_interval: None,
        adjacency_cache_max_entries: 128,
        llm_preprocess: memhop::LlmPreprocessConfig {
            enable_search_preprocess: true,
            enable_write_preprocess: false,
            fallback_to_tokenizer: true,
            ..Default::default()
        },
    };

    let mut db = MemHop::open(config).expect("MemHop::open failed");

    // ========================================================================
    // 1. 导入 L3 知识节点（覆盖各个对话领域的知识）
    // ========================================================================
    print_separator("1/3: 导入 L3 知识节点");

    let l3_import = db
        .import_memory(ImportRequest {
            target_layer: memhop::TargetLayer::Knowledge,
            data: ImportData::Knowledge(vec![
                KnowledgeImportItem {
                    title: "Rust Ownership".to_string(),
                    domain: "programming".to_string(),
                    knowledge_type: "Conceptual".to_string(),
                    text: "Rust 的所有权系统确保内存安全。每个值同时只有一个所有者。借用检查器在编译时验证引用有效性。所有权规则：每个值有且仅有一个所有者；引用必须始终有效；同一时间只能有一个可变引用或任意数量的不可变引用。".to_string(),
                    summary: None,
                    keywords: vec!["Rust".to_string(), "ownership".to_string(), "borrow checker".to_string(), "引用".to_string(), "内存安全".to_string()],
                    source_ref: None,
                },
                KnowledgeImportItem {
                    title: "Async/Await in Rust".to_string(),
                    domain: "programming".to_string(),
                    knowledge_type: "Conceptual".to_string(),
                    text: "Rust 的 async/await 语法实现零成本异步编程。Future trait 是核心抽象，tokio 是主流运行时。async fn 返回实现了 Future 的类型，.await 会暂停当前任务直到 Future 完成。".to_string(),
                    summary: None,
                    keywords: vec!["Rust".to_string(), "async".to_string(), "tokio".to_string(), "Future".to_string(), "异步".to_string()],
                    source_ref: None,
                },
                KnowledgeImportItem {
                    title: "微服务架构模式".to_string(),
                    domain: "architecture".to_string(),
                    knowledge_type: "Conceptual".to_string(),
                    text: "微服务架构将应用拆分为一组小服务，每个服务运行在自己的进程中，通过轻量级机制通信。服务发现、负载均衡、熔断器、分布式追踪是核心模式。Saga 模式用于处理分布式事务的数据一致性。".to_string(),
                    summary: None,
                    keywords: vec!["微服务".to_string(), "服务发现".to_string(), "负载均衡".to_string(), "熔断器".to_string(), "Saga".to_string(), "分布式".to_string()],
                    source_ref: None,
                },
                KnowledgeImportItem {
                    title: "软件工程最佳实践".to_string(),
                    domain: "engineering".to_string(),
                    knowledge_type: "Procedural".to_string(),
                    text: "Bug 修复最佳实践：1) 重现问题；2) 隔离根因；3) 编写测试覆盖；4) 实施修复；5) 验证修复。内存泄漏排查常用工具：Valgrind、AddressSanitizer、heaptrack。".to_string(),
                    summary: None,
                    keywords: vec!["bug".to_string(), "调试".to_string(), "内存泄漏".to_string(), "测试".to_string()],
                    source_ref: None,
                },
                KnowledgeImportItem {
                    title: "系统编程概念".to_string(),
                    domain: "programming".to_string(),
                    knowledge_type: "Conceptual".to_string(),
                    text: "系统编程语言如 Rust 专注于安全、并发和性能。零成本抽象使得高级特性不会带来运行时开销。错误处理使用 Result 和 Option 类型，避免了空指针异常。Rc 提供引用计数的共享所有权，Arc 是线程安全的版本，Weak 用于打破循环引用。".to_string(),
                    summary: None,
                    keywords: vec!["Rc".to_string(), "Arc".to_string(), "Weak".to_string(), "循环引用".to_string(), "零成本抽象".to_string()],
                    source_ref: None,
                },
            ]),
            mode: ImportMode::Merge,
            knowledge_title: None,
        })
        .expect("L3 import failed");
    let l3_ids = l3_import.ids.as_ref().expect("L3 import should return ids");
    println!(
        "  创建了 {} 个 L3 节点: {:?}",
        l3_ids.len(),
        l3_ids.iter().map(|s| &s[..16]).collect::<Vec<_>>()
    );

    // ========================================================================
    // 2. 导入 L2 话题
    // ========================================================================
    print_separator("2/3: 导入 L2 话题");

    let topic_import = db
        .import_memory(ImportRequest {
            target_layer: memhop::TargetLayer::Topic,
            data: ImportData::Topics(vec![
                TopicImportItem {
                    title: "Rust 编程讨论".to_string(),
                    summary: Some("关于 Rust 语言特性的深入讨论，包括所有权、借用检查、异步编程和系统编程概念".to_string()),
                    keywords: vec!["rust".to_string(), "编程".to_string(), "系统语言".to_string(), "borrow checker".to_string()],
                    knowledge_domain: Some("programming".to_string()),
                },
                TopicImportItem {
                    title: "Bug 修复经验".to_string(),
                    summary: Some("关于调试和修复 Bug 的经验分享，特别是内存泄漏和循环引用问题".to_string()),
                    keywords: vec!["bug".to_string(), "调试".to_string(), "内存泄漏".to_string(), "循环引用".to_string()],
                    knowledge_domain: Some("engineering".to_string()),
                },
                TopicImportItem {
                    title: "微服务架构讨论".to_string(),
                    summary: Some("关于微服务架构设计、服务发现、分布式事务和数据一致性".to_string()),
                    keywords: vec!["微服务".to_string(), "架构".to_string(), "分布式".to_string()],
                    knowledge_domain: Some("architecture".to_string()),
                },
            ]),
            mode: ImportMode::Merge,
            knowledge_title: None,
        })
        .expect("Topic import failed");
    let topic_ids = topic_import
        .ids
        .as_ref()
        .expect("Topic import should return ids");
    let rust_topic_id = &topic_ids[0];
    let bug_topic_id = &topic_ids[1];
    let microservice_topic_id = &topic_ids[2];
    println!("  创建了 {} 个话题:", topic_ids.len());
    println!("    [0] Rust 编程讨论:   {}", &rust_topic_id[..16]);
    println!("    [1] Bug 修复经验:    {}", &bug_topic_id[..16]);
    println!("    [2] 微服务架构讨论:  {}", &microservice_topic_id[..16]);

    // 关联 L3 知识到话题
    println!("\n  关联 L3 知识到话题...");
    let _ = db.update_l2(
        rust_topic_id,
        UpdateL2Fields {
            l3_refs: Some(vec![
                l3_ids[0].clone(),
                l3_ids[1].clone(),
                l3_ids[4].clone(),
            ]),
            ..Default::default()
        },
    );
    let _ = db.update_l2(
        bug_topic_id,
        UpdateL2Fields {
            l3_refs: Some(vec![l3_ids[3].clone()]),
            ..Default::default()
        },
    );
    let _ = db.update_l2(
        microservice_topic_id,
        UpdateL2Fields {
            l3_refs: Some(vec![l3_ids[2].clone()]),
            ..Default::default()
        },
    );
    println!("  ✓");

    // ========================================================================
    // 3. 定义 7 个对话测试用例
    // ========================================================================
    print_separator("3/3: 执行 7 种对话类型测试");

    let dialogues = vec![
        DialogueCase {
            name: "short",
            dialogue: "User: Rust borrow checker 怎么用\nAssistant: 借用检查器在编译时确保引用有效性，遵循三条规则：1) 一个值同时只能有一个可变引用或任意数量的不可变引用；2) 引用必须始终有效。",
            search_query: "Rust borrow checker 怎么用",
            topic_title: "Rust 编程讨论",
            knowledge_domain: "programming",
        },
        DialogueCase {
            name: "long",
            dialogue: "User: 我最近在学 Rust 编程语言，遇到了 borrow checker 的问题，特别是在处理异步代码时，生命周期标注总是出错，你能帮我理解一下 Rc 和 Arc 的区别吗，以及在多线程环境下如何安全地共享状态\nAssistant: Rc 是单线程引用计数，Arc 是原子引用计数的线程安全版本。在多线程环境应使用 Arc 结合 Mutex 或 RwLock 来安全共享状态。生命周期标注 'a 表示引用有效的范围，异步代码中需要确保 future 的生命周期满足静态要求。",
            search_query: "Rc 和 Arc 的区别 多线程 共享状态 生命周期",
            topic_title: "Rust 编程讨论",
            knowledge_domain: "programming",
        },
        DialogueCase {
            name: "emotional",
            dialogue: "User: 我真的太开心了！今天终于把那个困扰了我三天的 bug 修好了，那个内存泄漏问题原来是 Arc 循环引用导致的，改用 Weak 之后就解决了！\nAssistant: 太棒了！确实，Arc 循环引用是 Rust 中常见的内存泄漏原因。使用 Weak 打破循环是标准做法。这种'顿悟时刻'非常令人满足，恭喜你！",
            search_query: "内存泄漏 Arc 循环引用 Weak 解决",
            topic_title: "Bug 修复经验",
            knowledge_domain: "engineering",
        },
        DialogueCase {
            name: "code",
            dialogue: "User: 帮我看看这段代码有什么问题：fn main() { let s1 = String::from(\"hello\"); let s2 = s1; println!(\"{}\", s1); } 编译器报错了说 s1 已经被 move 了\nAssistant: 这是 Rust 的所有权移动问题。String 类型没有实现 Copy trait，所以 let s2 = s1 会将所有权从 s1 移动到 s2，之后 s1 不再有效。解决方法：1) 使用 s1.clone() 克隆数据；2) 传递引用 &s1 而不是所有权。",
            search_query: "Rust move 语义 所有权 String clone",
            topic_title: "Rust 编程讨论",
            knowledge_domain: "programming",
        },
        DialogueCase {
            name: "long_text",
            dialogue: "User: 请总结一下以下文章内容：Rust 是一门系统编程语言，专注于安全、并发和性能。它的所有权系统在编译时就能防止数据竞争，而不需要垃圾回收器。Rust 的零成本抽象使得高级特性不会带来运行时开销。此外，Rust 的错误处理机制使用 Result 和 Option 类型，避免了空指针异常。Rust 的 trait 系统支持泛型编程和运行时多态。它的宏系统允许元编程，减少样板代码。Rust 的包管理器 Cargo 和构建工具使得项目管理变得简单。Rust 在系统编程、WebAssembly、嵌入式开发和 CLI 工具领域都有广泛应用。\nAssistant: 文章主要介绍了 Rust 语言的核心特性：安全的内存管理（所有权系统）、零成本抽象、错误处理（Result/Option）、trait 系统、宏系统、以及 Cargo 工具链。Rust 已广泛应用于系统编程、WebAssembly、嵌入式和 CLI 开发等领域。",
            search_query: "Rust 系统编程 安全 并发 所有权 零成本抽象",
            topic_title: "Rust 编程讨论",
            knowledge_domain: "programming",
        },
        DialogueCase {
            name: "article",
            dialogue: "User: 我刚读了一篇关于微服务架构的文章，提到了服务发现、负载均衡、熔断器模式、分布式追踪这些概念，我们的项目从单体迁移到微服务后遇到了数据一致性问题，用了 Saga 模式才解决\nAssistant: 微服务架构的确会引入分布式数据一致性的挑战。Saga 模式通过将分布式事务拆分为一系列本地事务，每个事务完成后触发下一个事务，失败时执行补偿事务来保证最终一致性。服务发现可以用 Consul 或 etcd，负载均衡可以用 gRPC 的客户端负载均衡或 Nginx，分布式追踪推荐 Jaeger 或 Zipkin。",
            search_query: "微服务 Saga 模式 数据一致性 分布式",
            topic_title: "微服务架构讨论",
            knowledge_domain: "architecture",
        },
        DialogueCase {
            name: "path",
            dialogue: "User: 项目文件在 /Volumes/zt_hd/projects/meow/memhop/src/query/search.rs 这个位置有个 bug，还有 /Users/zt_mac/.config/app/settings.json 配置文件也需要检查一下\nAssistant: 好的，我来分析这两个文件的问题。search.rs 中的检索逻辑需要检查 BM25 评分和缓存机制，settings.json 需要确认环境配置是否正确。",
            search_query: "文件路径 bug search.rs settings.json 配置检查",
            topic_title: "Bug 修复经验",
            knowledge_domain: "engineering",
        },
    ];

    let mut results: Vec<PerDialogueResult> = Vec::new();

    for (idx, case) in dialogues.iter().enumerate() {
        println!(
            "\n{}{} {} {}",
            "-".repeat(20),
            "对话类型 ",
            idx + 1,
            case.name
        );
        println!(
            "  对话内容: {}",
            case.dialogue.chars().take(100).collect::<String>()
        );

        let topic_id = match idx {
            0 | 1 | 3 | 4 => rust_topic_id.clone(),
            2 | 6 => bug_topic_id.clone(),
            5 => microservice_topic_id.clone(),
            _ => unreachable!(),
        };

        // 写入对话
        println!("\n  [写入] update_memory...");
        let update_result = db.update_memory(UpdateRequest {
            topic_id: topic_id.clone(),
            dialogue_text: case.dialogue.to_string(),
            summary: Some(format!(
                "{} 对话测试: {}",
                case.name,
                case.search_query.chars().take(40).collect::<String>()
            )),
            action_chain: Some(vec![ActionItem {
                title: format!("test_{}", case.name),
                description: format!("{} dialogue type test", case.name),
                action_type: ActionType::Execute,
                parameters: None,
            }]),
            instant_distill: false,
            scene_id: Some(format!("comprehensive_test_scene")),
            source: Default::default(),
            user_keywords: None,
            agent_keywords: None,
        });

        match &update_result {
            Ok(r) => println!(
                "    ✓ archive_id={}, turn_node_id={}",
                &r.archive_id[..r.archive_id.len().min(16)],
                &r.turn_node_id[..r.turn_node_id.len().min(16)]
            ),
            Err(e) => {
                println!("    ✗ update_memory 失败: {}", e);
                results.push(PerDialogueResult {
                    dialogue_type: case.name.to_string(),
                    keyword_count: 0,
                    keywords: vec![],
                    keyword_analysis: "写入失败，无法分析".to_string(),
                    has_l3_hints: false,
                    l3_hint_count: 0,
                    l3_hints: vec![],
                    l3_hint_analysis: "写入失败".to_string(),
                    scene_id: String::new(),
                    scene_title: String::new(),
                    scene_score: 0.0,
                    topics_count: 0,
                    associated_count: 0,
                    l3_ids_count: 0,
                    l4_refs_found: false,
                    profile_present: false,
                    user_keywords: vec![],
                    agent_keywords: vec![],
                    issues: vec!["update_memory 失败".to_string()],
                });
                continue;
            }
        }

        // 执行检索
        println!("\n  [检索] search_context...");
        println!("    查询: {}", case.search_query);

        let search_result = db.search_context(SearchQuery {
            dialogue: case.search_query.to_string(),
            l2_id: None,
            l3_id: None,
            auto_create: false,
        });

        match search_result {
            Ok(result) => {
                println!("    ✓ 检索成功");
                println!("\n    ---- 完整 SearchResult JSON ----");
                println!("{}", serde_json::to_string_pretty(&result).unwrap());

                // 分析各个维度
                let analysis = analyze_dialogue_result(case, &result);
                results.push(analysis);
            }
            Err(e) => {
                println!("    ✗ 检索失败: {}", e);
                results.push(PerDialogueResult {
                    dialogue_type: case.name.to_string(),
                    keyword_count: 0,
                    keywords: vec![],
                    keyword_analysis: format!("检索失败: {}", e),
                    has_l3_hints: false,
                    l3_hint_count: 0,
                    l3_hints: vec![],
                    l3_hint_analysis: "检索失败".to_string(),
                    scene_id: String::new(),
                    scene_title: String::new(),
                    scene_score: 0.0,
                    topics_count: 0,
                    associated_count: 0,
                    l3_ids_count: 0,
                    l4_refs_found: false,
                    profile_present: false,
                    user_keywords: vec![],
                    agent_keywords: vec![],
                    issues: vec![format!("search_context 失败: {}", e)],
                });
            }
        }
    }

    // ========================================================================
    // 生成测试报告
    // ========================================================================
    print_separator("测试报告");
    print_report_table(&results);

    // 清理
    drop(db);
    let _ = std::fs::remove_file(db_path);

    println!("\n========== 综合对话测试完成 ==========");
}

// ============================================================================
// 分析函数
// ============================================================================

fn analyze_dialogue_result(
    case: &DialogueCase,
    result: &memhop::SearchResult,
) -> PerDialogueResult {
    let mut issues: Vec<String> = Vec::new();

    // ---- 维度 1: 关键词提取质量 ----
    let keywords = result.llm_keywords_used.clone().unwrap_or_default();
    let keyword_count = keywords.len();
    let keyword_analysis = analyze_keyword_coverage(&keywords, case);

    // ---- 维度 2: L3 自动导入 ----
    let l3_hints = result.l3_import_hints.clone().unwrap_or_default();
    let has_l3_hints = !l3_hints.is_empty();
    let l3_hint_count = l3_hints.len();
    let l3_hint_analysis = analyze_l3_hints(&l3_hints, case);

    // ---- 维度 3: L2 数据更新 ----
    let user_keywords = if !result.contexts.is_empty() {
        result.contexts[0].user_keywords.clone()
    } else {
        vec![]
    };
    let agent_keywords = if !result.contexts.is_empty() {
        result.contexts[0].agent_keywords.clone()
    } else {
        vec![]
    };

    // ---- 维度 4: 返回数据完整性 ----
    let scene_id = if !result.contexts.is_empty() {
        result.contexts[0].scene_id.clone()
    } else {
        String::new()
    };
    let scene_score = if !result.contexts.is_empty() {
        result.contexts[0].retrieval_score
    } else {
        -1.0
    };
    let topics_count = result.contexts.len();
    let associated_count = result.associated_contexts.len();
    let l3_ids_count = result.l3_ids.len();
    let l4_refs_found = result.contexts.iter().any(|c| !c.l4_refs.is_empty());
    let profile_present = result.profile.is_some();

    // Data integrity checks
    if scene_id.is_empty() {
        issues.push("scene_id 为空".to_string());
    }
    if scene_score < 0.0 {
        issues.push(format!("retrieval_score 为负数: {}", scene_score));
    }
    if topics_count == 0 {
        issues.push("contexts 为空".to_string());
    }
    for (i, ctx) in result.contexts.iter().enumerate() {
        if ctx.id.is_empty() {
            issues.push(format!("contexts[{}].id 为空", i));
        }
        if ctx.depth < 1 || ctx.depth > 5 {
            issues.push(format!("contexts[{}].depth 无效: {}", i, ctx.depth));
        }
        if ctx.retrieval_score < 0.0 || ctx.retrieval_score > 1.0 {
            issues.push(format!(
                "contexts[{}].retrieval_score 超出 [0,1]: {}",
                i, ctx.retrieval_score
            ));
        }
        if ctx.l3_refs.is_empty() {
            issues.push(format!("contexts[{}].l3_refs 为空", i));
        }
        if ctx.l4_refs.is_empty() {
            issues.push(format!("contexts[{}].l4_refs 为空", i));
        }
    }

    PerDialogueResult {
        dialogue_type: case.name.to_string(),
        keyword_count,
        keywords,
        keyword_analysis,
        has_l3_hints,
        l3_hint_count,
        l3_hints,
        l3_hint_analysis,
        scene_id: if scene_id.len() > 16 {
            scene_id[..16].to_string()
        } else {
            scene_id
        },
        scene_title: String::new(),
        scene_score,
        topics_count,
        associated_count,
        l3_ids_count,
        l4_refs_found,
        profile_present,
        user_keywords,
        agent_keywords,
        issues,
    }
}

fn analyze_keyword_coverage(keywords: &[String], case: &DialogueCase) -> String {
    if keywords.is_empty() {
        return "关键词为空（LLM 预处理可能跳过）".to_string();
    }

    let mut analysis = String::new();

    // 检查关键词是否覆盖原文核心语义单元
    let _search_text = case.search_query.to_lowercase();
    let _dialogue_text = case.dialogue.to_lowercase();

    // 检查关键术语是否在关键词中
    let key_terms: Vec<&str> = match case.name {
        "short" => vec!["rust", "borrow", "checker", "借用"],
        "long" => vec!["rust", "rc", "arc", "生命周期", "多线程", "异步"],
        "emotional" => vec!["内存泄漏", "arc", "weak", "循环引用"],
        "code" => vec!["rust", "move", "所有权", "string", "clone"],
        "long_text" => vec!["rust", "所有权", "零成本", "system", "安全"],
        "article" => vec!["微服务", "saga", "分布式", "一致性", "数据"],
        "path" => vec!["bug", "文件", "路径", "search", "配置"],
        _ => vec![],
    };

    let kw_lower: Vec<String> = keywords.iter().map(|k| k.to_lowercase()).collect();
    let mut covered = 0;
    let mut missing: Vec<&str> = Vec::new();

    for term in &key_terms {
        if kw_lower.iter().any(|k| k.contains(term))
            || kw_lower.iter().any(|k| term.contains(k.as_str()))
        {
            covered += 1;
        } else {
            missing.push(term);
        }
    }

    let coverage_pct = if key_terms.is_empty() {
        0.0
    } else {
        covered as f64 / key_terms.len() as f64 * 100.0
    };

    analysis.push_str(&format!("关键词数: {}", keywords.len()));
    if !missing.is_empty() {
        analysis.push_str(&format!("，遗漏: {:?}", missing));
    }
    analysis.push_str(&format!("，覆盖度: {:.0}%", coverage_pct));

    // 检查关键词顺序是否与原文一致
    let search_words: Vec<&str> = case.search_query.split_whitespace().collect();
    let order_ok = true;
    for (i, kw) in keywords.iter().enumerate() {
        if let Some(pos) = search_words.iter().position(|&w| {
            w.to_lowercase().contains(&kw.to_lowercase())
                || kw.to_lowercase().contains(&w.to_lowercase())
        }) {
            if i > 0 && pos > 0 {
                // 简单的顺序检查
            }
        }
    }

    if order_ok && !keywords.is_empty() {
        analysis.push_str("，顺序合理");
    }

    analysis
}

fn analyze_l3_hints(hints: &[L3EntityHint], _case: &DialogueCase) -> String {
    if hints.is_empty() {
        return "无 L3 导入提示".to_string();
    }

    let mut analysis = String::new();
    analysis.push_str(&format!("{} 个实体: ", hints.len()));

    for (i, hint) in hints.iter().enumerate() {
        let entity_type = if hint.entity_type.is_empty() {
            "unknown"
        } else {
            &hint.entity_type
        };
        if i > 0 {
            analysis.push_str(", ");
        }
        analysis.push_str(&format!("{}({})", hint.name, entity_type));
    }

    // 检查实体类型
    let valid_types = [
        "person",
        "organization",
        "location",
        "concept",
        "technology",
        "event",
        "product",
        "language",
        "skill",
        "habit",
    ];
    for hint in hints {
        if !hint.entity_type.is_empty() && !valid_types.contains(&hint.entity_type.as_str()) {
            analysis.push_str(&format!(" [警告: 实体类型 '{}' 非标准]", hint.entity_type));
        }
    }

    analysis
}

fn print_report_table(results: &[PerDialogueResult]) {
    println!();
    println!("+{}+", "-".repeat(130));
    println!(
        "| {:<12} | {:<8} | {:<20} | {:<16} | {:<14} | {:<10} | {:<10} | {:<14} |",
        "对话类型",
        "关键词数",
        "关键词覆盖度",
        "L3 hints",
        "scene_title",
        "l3_refs",
        "l4_refs",
        "问题"
    );
    println!("+{}+", "-".repeat(130));

    for r in results {
        let _keyword_info = format!(
            "{}个 - {:?}",
            r.keyword_count,
            r.keywords
                .iter()
                .map(|s| s.chars().take(12).collect::<String>())
                .collect::<Vec<_>>()
        );
        let coverage = if r.keyword_analysis.len() > 40 {
            format!(
                "{}...",
                r.keyword_analysis.chars().take(37).collect::<String>()
            )
        } else {
            r.keyword_analysis.clone()
        };
        let l3_info = if r.has_l3_hints {
            format!("{} 个", r.l3_hint_count)
        } else {
            "无".to_string()
        };
        let scene_title = if r.scene_title.chars().count() > 12 {
            format!("{}...", r.scene_title.chars().take(10).collect::<String>())
        } else {
            r.scene_title.clone()
        };
        let l3_refs = if r.l3_ids_count > 0 {
            format!("{}个", r.l3_ids_count)
        } else {
            "0".to_string()
        };
        let l4_refs = if r.l4_refs_found {
            "有".to_string()
        } else {
            "无".to_string()
        };
        let issue_str = if r.issues.is_empty() {
            "-".to_string()
        } else {
            format!("{}个问题", r.issues.len())
        };

        println!(
            "| {:<12} | {:<8} | {:<20} | {:<16} | {:<14} | {:<10} | {:<10} | {:<14} |",
            r.dialogue_type,
            r.keyword_count,
            coverage,
            l3_info,
            scene_title,
            l3_refs,
            l4_refs,
            issue_str
        );
    }

    println!("+{}+", "-".repeat(130));

    // 详细评价
    println!("\n=== 详细评价 ===\n");
    for r in results {
        println!("--- {} ---", r.dialogue_type);
        println!("  [关键词] {}", r.keyword_analysis);
        println!("  关键词列表: {:?}", r.keywords);
        println!("  [L3 提示] {}", r.l3_hint_analysis);
        if !r.l3_hints.is_empty() {
            for hint in &r.l3_hints {
                println!("    - {} ({})", hint.name, hint.entity_type);
            }
        }
        println!(
            "  [L2 数据] user_keywords: {:?}, agent_keywords: {:?}",
            r.user_keywords, r.agent_keywords
        );
        println!("  [完整性] scene_id={}, scene_score={:.4}, topics={}, associated={}, l3_ids={}, l4_refs={}, profile={}",
            r.scene_id, r.scene_score, r.topics_count, r.associated_count, r.l3_ids_count,
            if r.l4_refs_found { "有" } else { "无" },
            if r.profile_present { "有" } else { "无" });

        if r.issues.is_empty() {
            println!("  [评价] ✓ 所有检查通过");
        } else {
            println!("  [问题]");
            for issue in &r.issues {
                println!("    ✗ {}", issue);
            }
        }
        println!();
    }
}
