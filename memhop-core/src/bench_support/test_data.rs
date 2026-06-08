//! 测试数据生成器 — 为基准测试提供标准化的合成数据。

use crate::types::StoreItem;

/// 生成标准测试用 StoreItem 列表。
pub fn generate_store_items(count: usize) -> Vec<StoreItem> {
    let topics = [
        "rust_programming",
        "python_data_science",
        "web_development",
        "machine_learning",
        "database_design",
        "cloud_computing",
        "devops",
        "security",
        "algorithm",
        "software_architecture",
    ];

    let texts = [
        "Rust is a systems programming language focused on safety, speed, and concurrency.",
        "Python is widely used for data science, machine learning, and web development.",
        "React and Vue.js are popular frameworks for building modern web interfaces.",
        "Neural networks and deep learning have revolutionized artificial intelligence.",
        "PostgreSQL and MongoDB represent different approaches to data storage.",
        "AWS, Azure, and GCP provide scalable cloud infrastructure services.",
        "CI/CD pipelines automate software delivery and deployment processes.",
        "Encryption and authentication are fundamental to application security.",
        "Binary search and hash tables are essential algorithmic building blocks.",
        "Microservices architecture enables scalable and maintainable systems.",
        "Ownership and borrowing are Rust's unique memory safety features.",
        "TensorFlow and PyTorch are the leading deep learning frameworks.",
        "GraphQL provides a flexible alternative to RESTful API design.",
        "Docker containers package applications for consistent deployment.",
        "Kubernetes orchestrates containerized applications at scale.",
    ];

    (0..count)
        .map(|i| {
            let topic = topics[i % topics.len()];
            let text = texts[i % texts.len()];
            StoreItem {
                text: format!("{} [doc_{}]", text, i),
                source: "benchmark".to_string(),
                turn_id: Some(format!("turn_{}", i)),
                session_id: Some(format!("session_{}", i % 5)),
                topic_label: Some(topic.to_string()),
                llm_keywords: Some(vec![
                    topic.replace('_', " "),
                    format!("keyword_{}", i % 20),
                ]),
                llm_compressed_summary: Some(format!(
                    "Summary of document {} about {}",
                    i, topic
                )),
                valence: Some(0.3 + (i as f64 * 0.01).min(0.6)),
                arousal: Some(0.2 + (i as f64 * 0.005).min(0.5)),
                chain_parent_id: if i > 0 && i % 3 == 0 {
                    Some(format!("node_{}", i - 1))
                } else {
                    None
                },
                chain_label: if i > 0 && i % 3 == 0 {
                    Some("follow_up".to_string())
                } else {
                    None
                },
                domain_id: None,
                importance: Some(0.5 + (i as f32 * 0.01).min(0.4)),
            }
        })
        .collect()
}

/// 生成多样化的查询列表。
pub fn generate_recall_queries(count: usize) -> Vec<String> {
    let queries = [
        "memory safety in programming",
        "web development frameworks",
        "machine learning algorithms",
        "database optimization techniques",
        "cloud deployment strategies",
        "security best practices",
        "container orchestration",
        "API design patterns",
        "performance optimization",
        "data processing pipelines",
        "Rust ownership system",
        "Python data analysis",
        "React component architecture",
        "Kubernetes scaling",
        "CI/CD automation",
    ];

    (0..count)
        .map(|i| queries[i % queries.len()].to_string())
        .collect()
}

/// 生成多话题混合数据（不同话题的文档数量不同，模拟真实分布）。
pub fn generate_mixed_topic_items(total_count: usize) -> Vec<StoreItem> {
    let topic_weights = [0.3, 0.25, 0.2, 0.15, 0.1]; // 5 个话题，不均匀分布
    let topic_names = ["rust", "python", "web", "ml", "devops"];
    let texts = [
        "Rust provides memory safety without garbage collection through its ownership system.",
        "Python excels at rapid prototyping and has extensive scientific computing libraries.",
        "Modern web applications use component-based architecture for better maintainability.",
        "Deep learning models require large datasets and significant computational resources.",
        "DevOps practices bridge the gap between development and operations teams.",
    ];

    let mut items = Vec::with_capacity(total_count);
    for (idx, (weight, topic)) in topic_weights.iter().zip(topic_names.iter()).enumerate() {
        let count = (total_count as f64 * weight) as usize;
        for i in 0..count {
            let global_idx = items.len();
            items.push(StoreItem {
                text: format!("{} [topic_{}_doc_{}]", texts[idx], topic, i),
                source: "benchmark".to_string(),
                turn_id: Some(format!("turn_{}", global_idx)),
                session_id: Some(format!("session_{}", global_idx % 10)),
                topic_label: Some(topic.to_string()),
                llm_keywords: Some(vec![
                    topic.to_string(),
                    format!("term_{}_{}", idx, i % 10),
                ]),
                llm_compressed_summary: None,
                valence: Some(0.5),
                arousal: Some(0.3),
                chain_parent_id: None,
                chain_label: None,
                domain_id: None,
                importance: Some(0.5),
            });
        }
    }
    items
}

/// 生成多会话数据。
pub fn generate_session_items(
    session_count: usize,
    turns_per_session: usize,
) -> Vec<StoreItem> {
    let session_topics = [
        "project_planning",
        "code_review",
        "bug_fixing",
        "architecture_design",
        "testing",
    ];

    let mut items = Vec::with_capacity(session_count * turns_per_session);
    for s in 0..session_count {
        let topic = session_topics[s % session_topics.len()];
        for t in 0..turns_per_session {
            items.push(StoreItem {
                text: format!(
                    "Session {} turn {}: discussing {} in detail",
                    s, t, topic
                ),
                source: "chat".to_string(),
                turn_id: Some(format!("s{}_t{}", s, t)),
                session_id: Some(format!("session_{}", s)),
                topic_label: Some(topic.to_string()),
                llm_keywords: None,
                llm_compressed_summary: None,
                valence: Some(0.5),
                arousal: Some(0.3),
                chain_parent_id: if t > 0 {
                    Some(format!("s{}_t{}", s, t - 1))
                } else {
                    None
                },
                chain_label: if t > 0 {
                    Some("continuation".to_string())
                } else {
                    None
                },
                domain_id: None,
                importance: Some(0.5),
            });
        }
    }
    items
}
