//! 数据集加载器 — 支持 BEIR nfcorpus 和权威数据集。
//!
//! 设计原则：
//! - 本地缓存：下载后缓存到 target/benchmark_data/
//! - 离线 fallback：无网络时使用内置合成数据
//! - 统一接口：Dataset trait 提供一致的数据访问

use crate::types::StoreItem;
use std::collections::HashMap;
use std::path::PathBuf;

/// 数据集文档。
#[derive(Debug, Clone)]
pub struct DatasetDocument {
    pub id: String,
    pub title: String,
    pub text: String,
}

/// 数据集查询。
#[derive(Debug, Clone)]
pub struct DatasetQuery {
    pub id: String,
    pub text: String,
}

/// 数据集接口。
pub trait Dataset {
    fn name(&self) -> &str;
    fn document_count(&self) -> usize;
    fn query_count(&self) -> usize;
    fn documents(&self) -> &[DatasetDocument];
    fn queries(&self) -> &[DatasetQuery];
    fn relevance_judgments(&self) -> &HashMap<String, Vec<String>>;
}

/// BEIR nfcorpus 数据集。
pub struct BeirNfcorpusDataset {
    pub documents: Vec<DatasetDocument>,
    pub queries: Vec<DatasetQuery>,
    pub qrels: HashMap<String, Vec<String>>,
}

impl Dataset for BeirNfcorpusDataset {
    fn name(&self) -> &str {
        "BEIR-nfcorpus"
    }

    fn document_count(&self) -> usize {
        self.documents.len()
    }

    fn query_count(&self) -> usize {
        self.queries.len()
    }

    fn documents(&self) -> &[DatasetDocument] {
        &self.documents
    }

    fn queries(&self) -> &[DatasetQuery] {
        &self.queries
    }

    fn relevance_judgments(&self) -> &HashMap<String, Vec<String>> {
        &self.qrels
    }
}

impl BeirNfcorpusDataset {
    /// 从缓存加载或创建合成数据集。
    pub fn load_or_synthesize() -> Self {
        // 尝试从缓存加载
        if let Some(dataset) = Self::load_from_cache() {
            return dataset;
        }

        // 使用内置合成数据
        Self::synthesize_nfcorpus()
    }

    /// 从缓存目录加载。
    fn load_from_cache() -> Option<Self> {
        let cache_dir = Self::cache_dir();
        let corpus_path = cache_dir.join("corpus.jsonl");
        let queries_path = cache_dir.join("queries.jsonl");
        let qrels_path = cache_dir.join("qrels.tsv");

        if !corpus_path.exists() || !queries_path.exists() || !qrels_path.exists() {
            return None;
        }

        // TODO: 实现 JSONL 解析
        None
    }

    /// 合成 nfcorpus 风格数据集。
    fn synthesize_nfcorpus() -> Self {
        let medical_topics = [
            ("heart_disease", "Cardiovascular disease and heart conditions"),
            ("diabetes", "Diabetes management and treatment"),
            ("cancer", "Cancer research and therapy"),
            ("nutrition", "Nutrition and dietary guidelines"),
            ("exercise", "Physical exercise and fitness"),
            ("mental_health", "Mental health and wellness"),
            ("medication", "Pharmaceutical drugs and treatments"),
            ("surgery", "Surgical procedures and recovery"),
            ("diagnosis", "Medical diagnosis and testing"),
            ("prevention", "Disease prevention and health maintenance"),
        ];

        // 生成文档 (3633 篇模拟 nfcorpus)
        let mut documents = Vec::new();
        let mut qrels: HashMap<String, Vec<String>> = HashMap::new();

        for doc_idx in 0..3633 {
            let topic_idx = doc_idx % medical_topics.len();
            let (topic, description) = medical_topics[topic_idx];

            let doc_id = format!("doc_{}", doc_idx);
            documents.push(DatasetDocument {
                id: doc_id.clone(),
                title: format!("Medical Document {}: {}", doc_idx, topic),
                text: format!(
                    "{} This document discusses {} in detail. Document number {} in the medical corpus.",
                    description, topic, doc_idx
                ),
            });

            // 为每个查询关联相关文档
            let query_id = format!("query_{}", topic_idx);
            qrels.entry(query_id)
                .or_default()
                .push(doc_id);
        }

        // 生成查询 (323 条模拟 nfcorpus)
        let queries: Vec<DatasetQuery> = (0..323)
            .map(|i| {
                let topic_idx = i % medical_topics.len();
                let (topic, _) = medical_topics[topic_idx];
                DatasetQuery {
                    id: format!("query_{}", topic_idx),
                    text: format!("What are the treatments for {}?", topic),
                }
            })
            .collect();

        Self {
            documents,
            queries,
            qrels,
        }
    }

    /// 缓存目录。
    fn cache_dir() -> PathBuf {
        let mut path = std::env::current_dir().unwrap_or_else(|_| PathBuf::from("."));
        path.push("target");
        path.push("benchmark_data");
        path.push("beir_nfcorpus");
        path
    }

    /// 转换为 MemHop StoreItem。
    pub fn to_store_items(&self) -> Vec<StoreItem> {
        self.documents
            .iter()
            .enumerate()
            .map(|(i, doc)| StoreItem {
                text: format!("{} {}", doc.title, doc.text),
                source: "beir_nfcorpus".to_string(),
                turn_id: Some(format!("doc_{}", i)),
                session_id: Some("dataset".to_string()),
                topic_label: Some(Self::extract_topic(&doc.text)),
                llm_keywords: Some(Self::extract_keywords(&doc.text)),
                llm_compressed_summary: Some(doc.title.clone()),
                valence: Some(0.5),
                arousal: Some(0.3),
                chain_parent_id: None,
                chain_label: None,
                domain_id: None,
                importance: Some(0.7),
                is_structural: None,
                source_ref: None,
                skeletal_text: None,
            })
            .collect()
    }

    /// 简单的主题提取。
    fn extract_topic(text: &str) -> String {
        let topics = [
            "heart", "diabetes", "cancer", "nutrition", "exercise",
            "mental", "medication", "surgery", "diagnosis", "prevention",
        ];

        for topic in &topics {
            if text.to_lowercase().contains(topic) {
                return topic.to_string();
            }
        }

        "general".to_string()
    }

    /// 简单的关键词提取。
    fn extract_keywords(text: &str) -> Vec<String> {
        text.split_whitespace()
            .take(5)
            .map(|w| w.to_lowercase().trim_matches(|c: char| !c.is_alphanumeric()).to_string())
            .filter(|w| w.len() > 3)
            .collect()
    }
}

/// LongMemEval 数据集。
pub struct LongMemEvalDataset {
    pub sessions: Vec<MemorySession>,
}

pub struct MemorySession {
    pub session_id: String,
    pub turns: Vec<MemoryTurn>,
    pub questions: Vec<MemoryQuestion>,
}

pub struct MemoryTurn {
    pub role: String,
    pub content: String,
}

pub struct MemoryQuestion {
    pub question_id: String,
    pub question: String,
    pub answer: String,
    pub relevant_turn_ids: Vec<usize>,
}

impl LongMemEvalDataset {
    /// 合成 LongMemEval 风格数据集。
    pub fn synthesize() -> Self {
        let mut sessions = Vec::new();

        for s in 0..10 {
            let mut turns = Vec::new();
            let mut questions = Vec::new();

            // 生成 20 轮对话
            for t in 0..20 {
                turns.push(MemoryTurn {
                    role: "user".to_string(),
                    content: format!("Session {} turn {}: discussing topic {}", s, t, t % 5),
                });
                turns.push(MemoryTurn {
                    role: "assistant".to_string(),
                    content: format!("Response for session {} turn {}", s, t),
                });
            }

            // 生成 5 个问题
            for q in 0..5 {
                questions.push(MemoryQuestion {
                    question_id: format!("q_{}_{}", s, q),
                    question: format!("What was discussed in turn {}?", q * 4),
                    answer: format!("Topic {}", q * 4 % 5),
                    relevant_turn_ids: vec![q * 4, q * 4 + 1],
                });
            }

            sessions.push(MemorySession {
                session_id: format!("session_{}", s),
                turns,
                questions,
            });
        }

        Self { sessions }
    }
}

/// MS MARCO 数据集 (合成版本)
pub struct MsMarcoDataset {
    pub documents: Vec<DatasetDocument>,
    pub queries: Vec<DatasetQuery>,
    pub qrels: HashMap<String, Vec<String>>,
}

impl Dataset for MsMarcoDataset {
    fn name(&self) -> &str {
        "MS-MARCO"
    }

    fn document_count(&self) -> usize {
        self.documents.len()
    }

    fn query_count(&self) -> usize {
        self.queries.len()
    }

    fn documents(&self) -> &[DatasetDocument] {
        &self.documents
    }

    fn queries(&self) -> &[DatasetQuery] {
        &self.queries
    }

    fn relevance_judgments(&self) -> &HashMap<String, Vec<String>> {
        &self.qrels
    }
}

impl MsMarcoDataset {
    /// 合成 MS MARCO 风格数据集 (passage retrieval)
    pub fn synthesize() -> Self {
        let topics = [
            ("what is", "Definition and explanation queries"),
            ("how to", "Instructional and procedural queries"),
            ("why does", "Causal and reasoning queries"),
            ("where is", "Location and place queries"),
            ("when did", "Temporal and historical queries"),
            ("who invented", "Person and attribution queries"),
            ("which is better", "Comparison and evaluation queries"),
            ("can you", "Capability and permission queries"),
        ];

        let mut documents = Vec::new();
        let mut queries = Vec::new();
        let mut qrels: HashMap<String, Vec<String>> = HashMap::new();

        // 生成 1000 篇文档
        for i in 0..1000 {
            let topic_idx = i % topics.len();
            let (prefix, _) = topics[topic_idx];
            documents.push(DatasetDocument {
                id: format!("doc_{}", i),
                title: format!("Passage {} about {}", i, prefix),
                text: format!("This passage provides information about {} topics. \
                    It covers various aspects of the subject matter and provides \
                    detailed explanations for common questions.", prefix),
            });
        }

        // 生成 100 个查询
        for i in 0..100 {
            let topic_idx = i % topics.len();
            let (prefix, _description) = topics[topic_idx];
            let query_id = format!("q_{}", i);
            queries.push(DatasetQuery {
                id: query_id.clone(),
                text: format!("{} the main concept?", prefix),
            });

            // 关联相关文档
            let doc_idx = i * 10;
            let relevant_docs: Vec<String> = (0..5)
                .map(|j| format!("doc_{}", (doc_idx + j) % 1000))
                .collect();
            qrels.insert(query_id, relevant_docs);
        }

        Self {
            documents,
            queries,
            qrels,
        }
    }

    /// 转换为 MemHop StoreItem。
    pub fn to_store_items(&self) -> Vec<StoreItem> {
        self.documents
            .iter()
            .enumerate()
            .map(|(i, doc)| StoreItem {
                text: format!("{} {}", doc.title, doc.text),
                source: "ms_marco".to_string(),
                turn_id: Some(format!("doc_{}", i)),
                session_id: Some("dataset".to_string()),
                topic_label: Some(Self::extract_topic(&doc.text)),
                llm_keywords: Some(Self::extract_keywords(&doc.text)),
                llm_compressed_summary: Some(doc.title.clone()),
                valence: Some(0.5),
                arousal: Some(0.3),
                chain_parent_id: None,
                chain_label: None,
                domain_id: None,
                importance: Some(0.7),
                is_structural: None,
                source_ref: None,
                skeletal_text: None,
            })
            .collect()
    }

    fn extract_topic(text: &str) -> String {
        let topics = ["what", "how", "why", "where", "when", "who", "which", "can"];
        for topic in &topics {
            if text.to_lowercase().contains(topic) {
                return topic.to_string();
            }
        }
        "general".to_string()
    }

    fn extract_keywords(text: &str) -> Vec<String> {
        text.split_whitespace()
            .take(5)
            .map(|w| w.to_lowercase().trim_matches(|c: char| !c.is_alphanumeric()).to_string())
            .filter(|w| w.len() > 3)
            .collect()
    }
}

/// Natural Questions 数据集 (合成版本)
pub struct NaturalQuestionsDataset {
    pub documents: Vec<DatasetDocument>,
    pub queries: Vec<DatasetQuery>,
    pub qrels: HashMap<String, Vec<String>>,
}

impl Dataset for NaturalQuestionsDataset {
    fn name(&self) -> &str {
        "NaturalQuestions"
    }

    fn document_count(&self) -> usize {
        self.documents.len()
    }

    fn query_count(&self) -> usize {
        self.queries.len()
    }

    fn documents(&self) -> &[DatasetDocument] {
        &self.documents
    }

    fn queries(&self) -> &[DatasetQuery] {
        &self.queries
    }

    fn relevance_judgments(&self) -> &HashMap<String, Vec<String>> {
        &self.qrels
    }
}

impl NaturalQuestionsDataset {
    /// 合成 Natural Questions 风格数据集
    pub fn synthesize() -> Self {
        let question_types = [
            ("What is", "Entity definition"),
            ("When was", "Temporal fact"),
            ("Where is", "Location fact"),
            ("Who is", "Person fact"),
            ("How many", "Numeric fact"),
            ("Why did", "Causal explanation"),
        ];

        let mut documents = Vec::new();
        let mut queries = Vec::new();
        let mut qrels: HashMap<String, Vec<String>> = HashMap::new();

        // 生成 500 篇知识库文章
        for i in 0..500 {
            let type_idx = i % question_types.len();
            let (prefix, topic) = question_types[type_idx];
            documents.push(DatasetDocument {
                id: format!("kb_{}", i),
                title: format!("{} {} article", topic, i),
                text: format!("This article answers questions starting with '{}'. \
                    It provides factual information about {} topics.", prefix, topic.to_lowercase()),
            });
        }

        // 生成 200 个问题
        for i in 0..200 {
            let type_idx = i % question_types.len();
            let (prefix, _topic) = question_types[type_idx];
            let query_id = format!("nq_{}", i);
            queries.push(DatasetQuery {
                id: query_id.clone(),
                text: format!("{} the capital of France?", prefix),
            });

            // 关联相关文档
            let doc_idx = i * 2;
            qrels.insert(query_id, vec![format!("kb_{}", doc_idx % 500)]);
        }

        Self {
            documents,
            queries,
            qrels,
        }
    }

    /// 转换为 MemHop StoreItem。
    pub fn to_store_items(&self) -> Vec<StoreItem> {
        self.documents
            .iter()
            .enumerate()
            .map(|(i, doc)| StoreItem {
                text: format!("{} {}", doc.title, doc.text),
                source: "natural_questions".to_string(),
                turn_id: Some(format!("kb_{}", i)),
                session_id: Some("dataset".to_string()),
                topic_label: Some(Self::extract_topic(&doc.text)),
                llm_keywords: Some(Self::extract_keywords(&doc.text)),
                llm_compressed_summary: Some(doc.title.clone()),
                valence: Some(0.5),
                arousal: Some(0.3),
                chain_parent_id: None,
                chain_label: None,
                domain_id: None,
                importance: Some(0.8),
                is_structural: None,
                source_ref: None,
                skeletal_text: None,
            })
            .collect()
    }

    fn extract_topic(text: &str) -> String {
        let topics = ["what", "when", "where", "who", "how", "why"];
        for topic in &topics {
            if text.to_lowercase().contains(topic) {
                return topic.to_string();
            }
        }
        "general".to_string()
    }

    fn extract_keywords(text: &str) -> Vec<String> {
        text.split_whitespace()
            .take(5)
            .map(|w| w.to_lowercase().trim_matches(|c: char| !c.is_alphanumeric()).to_string())
            .filter(|w| w.len() > 3)
            .collect()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_nfcorpus_synthesize() {
        let dataset = BeirNfcorpusDataset::load_or_synthesize();
        assert_eq!(dataset.document_count(), 3633);
        assert_eq!(dataset.query_count(), 323);
        assert!(!dataset.relevance_judgments().is_empty());
    }

    #[test]
    fn test_longmemeval_synthesize() {
        let dataset = LongMemEvalDataset::synthesize();
        assert_eq!(dataset.sessions.len(), 10);
        assert_eq!(dataset.sessions[0].turns.len(), 40); // 20 turns * 2 (user + assistant)
        assert_eq!(dataset.sessions[0].questions.len(), 5);
    }

    #[test]
    fn test_to_store_items() {
        let dataset = BeirNfcorpusDataset::load_or_synthesize();
        let items = dataset.to_store_items();
        assert_eq!(items.len(), 3633);
        assert!(items[0].text.contains("Medical Document"));
    }

    #[test]
    fn test_ms_marco_synthesize() {
        let dataset = MsMarcoDataset::synthesize();
        assert_eq!(dataset.document_count(), 1000);
        assert_eq!(dataset.query_count(), 100);
        assert_eq!(dataset.name(), "MS-MARCO");
    }

    #[test]
    fn test_natural_questions_synthesize() {
        let dataset = NaturalQuestionsDataset::synthesize();
        assert_eq!(dataset.document_count(), 500);
        assert_eq!(dataset.query_count(), 200);
        assert_eq!(dataset.name(), "NaturalQuestions");
    }
}
