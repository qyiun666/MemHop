//! 合成数据生成器模块
//!
//! 用于生成基准测试所需的合成数据集，支持规模扩展性测试。
//! 模拟真实对话分布：短消息 60%、中等 30%、长文本 10%。
//! 使用确定性随机种子保证可复现性。

#![allow(dead_code, unused_imports)]

use rand::rngs::StdRng;
use rand::{Rng, SeedableRng};
use serde::{Deserialize, Serialize};

/// 生成的数据项
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct GeneratedItem {
    /// 文本内容
    pub text: String,
    /// 主题标签
    pub topic: String,
    /// 会话ID
    pub session_id: String,
    /// 时间戳（毫秒）
    pub timestamp: u64,
}

/// 生成的问题
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct GeneratedQuestion {
    /// 查询文本
    pub query: String,
    /// 期望答案
    pub expected_answer: String,
    /// 相关会话ID列表
    pub relevant_session_ids: Vec<String>,
    /// 问题类别
    pub category: String,
}

/// 主题模板
const TOPICS: &[(&str, &[&str])] = &[
    (
        "Rust编程",
        &[
            "所有权系统确保内存安全",
            "生命周期标注帮助编译器验证引用有效性",
            "trait 系统提供了强大的抽象能力",
            "模式匹配让代码更清晰",
            "错误处理使用 Result 类型",
        ],
    ),
    (
        "机器学习",
        &[
            "神经网络通过反向传播学习",
            "卷积网络擅长图像识别",
            "Transformer 架构革新了 NLP",
            "强化学习用于游戏和机器人",
            "迁移学习减少训练数据需求",
        ],
    ),
    (
        "数据库设计",
        &[
            "索引优化查询性能",
            "事务确保数据一致性",
            "分片提高可扩展性",
            "缓存减少数据库压力",
            "规范化减少数据冗余",
        ],
    ),
    (
        "系统架构",
        &[
            "微服务提高系统灵活性",
            "消息队列解耦服务依赖",
            "负载均衡分散请求压力",
            "熔断器防止级联故障",
            "服务网格管理服务通信",
        ],
    ),
    (
        "前端开发",
        &[
            "组件化提高代码复用",
            "虚拟 DOM 优化渲染性能",
            "响应式设计适配多种设备",
            "状态管理简化数据流",
            "SSR 改善首屏加载速度",
        ],
    ),
];

/// 短消息模板（60%）
const SHORT_TEMPLATES: &[&str] = &[
    "我觉得{}很重要",
    "关于{}有什么建议？",
    "如何学习{}？",
    "{}的最佳实践是什么？",
    "解释一下{}",
];

/// 中等消息模板（30%）
const MEDIUM_TEMPLATES: &[&str] = &[
    "在实际项目中应用{}时，我发现需要考虑多个方面。首先是性能，其次是可维护性。",
    "{}是一个复杂的主题，需要深入理解其核心概念才能正确使用。",
    "我最近在研究{}，发现它有很多有趣的特性和应用场景。",
    "团队在讨论{}时产生了分歧，我们需要找到一个平衡点。",
    "{}的发展趋势值得关注，它可能会改变我们的方式。",
];

/// 长文本模板（10%）
const LONG_TEMPLATES: &[&str] = &[
    "关于{}，这是一个值得深入探讨的话题。在实际应用中，我们需要考虑多个维度：\
     首先是技术可行性，其次是性能影响，然后是维护成本，最后是团队的学习曲线。\
     通过合理的架构设计，我们可以在这些维度之间找到平衡点。",
    "{}在现代软件开发中扮演着重要角色。从历史发展来看，它经历了多次演变。\
     早期的实现方式相对简单，但随着需求的增长，复杂度也在增加。\
     现在的解决方案更加成熟，但仍需要根据具体场景进行选择。",
    "我花了很长时间研究{}，总结出以下几点经验：第一，不要过度设计；\
     第二，优先考虑可读性；第三，做好性能测试；第四，编写完善的文档。\
     这些原则帮助我在多个项目中取得了成功。",
];

/// 问题模板
const QUESTION_TEMPLATES: &[(&str, &str)] = &[
    ("什么是{}的核心概念？", "概念"),
    ("{}的主要应用场景有哪些？", "应用"),
    ("如何优化{}的性能？", "优化"),
    ("{}有哪些常见问题？", "问题"),
    ("学习{}的最佳路径是什么？", "学习"),
];

/// 生成数据集
///
/// # 参数
/// - `size`: 生成的数据项数量
///
/// # 返回
/// (Vec<GeneratedItem>, Vec<GeneratedQuestion>) 元组
///
/// # 特点
/// - 使用确定性随机种子（42）保证可复现
/// - 模拟真实对话分布：短消息 60%、中等 30%、长文本 10%
/// - 生成对应的测试问题和期望答案
pub fn generate_dataset(size: usize) -> (Vec<GeneratedItem>, Vec<GeneratedQuestion>) {
    let mut rng = StdRng::seed_from_u64(42);
    let mut items = Vec::with_capacity(size);
    let mut questions = Vec::new();

    let base_timestamp: u64 = 1700000000000; // 2023-11-14

    for i in 0..size {
        // 随机选择主题
        let (topic_name, facts) = TOPICS[rng.gen_range(0..TOPICS.len())];
        let fact = facts[rng.gen_range(0..facts.len())];

        // 根据分布生成不同长度的文本
        let roll: f64 = rng.gen();
        let text = if roll < 0.6 {
            // 短消息 60%
            let template = SHORT_TEMPLATES[rng.gen_range(0..SHORT_TEMPLATES.len())];
            template.replace("{}", fact)
        } else if roll < 0.9 {
            // 中等消息 30%
            let template = MEDIUM_TEMPLATES[rng.gen_range(0..MEDIUM_TEMPLATES.len())];
            template.replace("{}", fact)
        } else {
            // 长文本 10%
            let template = LONG_TEMPLATES[rng.gen_range(0..LONG_TEMPLATES.len())];
            template.replace("{}", fact)
        };

        // 生成会话ID（模拟多个会话）
        let session_num = rng.gen_range(1..=10);
        let session_id = format!("session_{:03}", session_num);

        // 时间戳递增（模拟真实时间流）
        let timestamp = base_timestamp + (i as u64) * 1000;

        items.push(GeneratedItem {
            text,
            topic: topic_name.to_string(),
            session_id,
            timestamp,
        });
    }

    // 为每个主题生成问题
    for (topic_name, _facts) in TOPICS {
        for (template, category) in QUESTION_TEMPLATES {
            let query = template.replace("{}", topic_name);

            // 找到相关会话
            let relevant_session_ids: Vec<String> = items
                .iter()
                .filter(|item| item.topic == *topic_name)
                .map(|item| item.session_id.clone())
                .collect::<std::collections::HashSet<_>>()
                .into_iter()
                .collect();

            // 生成期望答案
            let expected_answer = format!("{}的核心要点包括：{}", topic_name, _facts.join("、"));

            questions.push(GeneratedQuestion {
                query,
                expected_answer,
                relevant_session_ids,
                category: category.to_string(),
            });
        }
    }

    (items, questions)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_generate_dataset_size() {
        let (items, questions) = generate_dataset(100);
        assert_eq!(items.len(), 100);
        assert!(!questions.is_empty());
    }

    #[test]
    fn test_generate_dataset_reproducible() {
        let (items1, questions1) = generate_dataset(50);
        let (items2, questions2) = generate_dataset(50);

        assert_eq!(items1.len(), items2.len());
        for (a, b) in items1.iter().zip(items2.iter()) {
            assert_eq!(a.text, b.text);
            assert_eq!(a.topic, b.topic);
            assert_eq!(a.session_id, b.session_id);
            assert_eq!(a.timestamp, b.timestamp);
        }

        assert_eq!(questions1.len(), questions2.len());
        for (a, b) in questions1.iter().zip(questions2.iter()) {
            assert_eq!(a.query, b.query);
            assert_eq!(a.expected_answer, b.expected_answer);
            assert_eq!(a.category, b.category);
        }
    }

    #[test]
    fn test_text_length_distribution() {
        let (items, _) = generate_dataset(1000);

        let short_count = items.iter().filter(|i| i.text.len() < 50).count();
        let medium_count = items
            .iter()
            .filter(|i| i.text.len() >= 50 && i.text.len() < 150)
            .count();
        let long_count = items.iter().filter(|i| i.text.len() >= 150).count();

        // 验证分布比例（允许一定误差）
        let total = items.len() as f64;
        let short_ratio = short_count as f64 / total;
        let medium_ratio = medium_count as f64 / total;
        let long_ratio = long_count as f64 / total;

        assert!(short_ratio > 0.5 && short_ratio < 0.7); // ~60%
        assert!(medium_ratio > 0.2 && medium_ratio < 0.4); // ~30%
        assert!(long_ratio > 0.05 && long_ratio < 0.15); // ~10%
    }

    #[test]
    fn test_topics_coverage() {
        let (items, _) = generate_dataset(500);
        let topics: std::collections::HashSet<&str> =
            items.iter().map(|i| i.topic.as_str()).collect();

        // 应该覆盖所有主题
        assert_eq!(topics.len(), TOPICS.len());
    }

    #[test]
    fn test_questions_have_relevant_sessions() {
        let (_, questions) = generate_dataset(100);
        for q in &questions {
            assert!(
                !q.relevant_session_ids.is_empty(),
                "Question '{}' should have relevant sessions",
                q.query
            );
        }
    }
}
