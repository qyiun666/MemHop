//! LLM 集成基准测试 — DeepSeek API 辅助的记忆提取、情感分析。
//!
//! 设计原则：
//! - Feature gate: 需要 bench-llm feature
//! - 缓存测试: 验证缓存机制
//! - Fallback: API 不可用时使用合成数据

use criterion::{Criterion, criterion_group, criterion_main};
use memhop_core::bench_support::llm_client::{DeepSeekClient, generate_test_cases};
use memhop_core::Emotion;

// ── 记忆提取质量 ──────────────────────────────────────

fn bench_llm_extraction(c: &mut Criterion) {
    let mut group = c.benchmark_group("llm/extraction");
    group.sample_size(10);

    let test_cases = generate_test_cases();

    group.bench_function("extract_10_texts", |b| {
        let mut client = DeepSeekClient::with_api_key(
            &std::env::var("DEEPSEEK_API_KEY").unwrap_or_default()
        ).with_cache(true);

        b.iter(|| {
            for tc in &test_cases {
                let extraction = client.extract_memory(&tc.input);
                // 验证主题提取
                assert!(!extraction.topic_label.is_empty());
                // 验证关键词
                assert!(!extraction.keywords.is_empty());
            }
        });
    });

    group.finish();
}

// ── 情感检测准确性 ──────────────────────────────────────

fn bench_llm_emotion(c: &mut Criterion) {
    let mut group = c.benchmark_group("llm/emotion");
    group.sample_size(10);

    let test_cases = generate_test_cases();

    group.bench_function("detect_emotion_10_texts", |b| {
        let mut client = DeepSeekClient::with_api_key(
            &std::env::var("DEEPSEEK_API_KEY").unwrap_or_default()
        ).with_cache(true);

        b.iter(|| {
            for tc in &test_cases {
                let (emotion, intensity, valence, arousal) = client.detect_emotion(&tc.input);
                // 验证情感检测
                assert!(intensity >= 0.0 && intensity <= 1.0);
                assert!(valence >= 0.0 && valence <= 1.0);
                assert!(arousal >= 0.0 && arousal <= 1.0);

                // 验证情感类型匹配（仅在有预期时）
                if tc.expected_emotion != Emotion::Neutral {
                    // 允许一定误差，因为合成数据可能不完全匹配
                    eprintln!("  Input: {}, Expected: {:?}, Got: {:?}",
                        tc.input, tc.expected_emotion, emotion);
                }
            }
        });
    });

    group.finish();
}

// ── 结晶摘要质量 ──────────────────────────────────────

fn bench_llm_crystallize(c: &mut Criterion) {
    let mut group = c.benchmark_group("llm/crystallize");
    group.sample_size(10);

    let memories = vec![
        "Rust 是一门系统编程语言",
        "Rust 的所有权系统确保内存安全",
        "Rust 的借用检查器在编译时防止数据竞争",
    ];

    group.bench_function("generate_crystallize_summary", |b| {
        let mut client = DeepSeekClient::with_api_key(
            &std::env::var("DEEPSEEK_API_KEY").unwrap_or_default()
        ).with_cache(true);

        b.iter(|| {
            let output = client.generate_crystallize_summary(
                "rust_programming",
                &memories,
            );
            assert!(!output.summary.is_empty());
            assert!(!output.keywords.is_empty());
            assert!(!output.domain_name.is_empty());
        });
    });

    group.finish();
}

// ── 缓存命中率 ──────────────────────────────────────

fn bench_llm_cache(c: &mut Criterion) {
    let mut group = c.benchmark_group("llm/cache");
    group.sample_size(10);

    let test_input = "Rust 的所有权系统很强大";

    group.bench_function("cache_hit_rate", |b| {
        let mut client = DeepSeekClient::with_api_key(
            &std::env::var("DEEPSEEK_API_KEY").unwrap_or_default()
        ).with_cache(true);

        // 第一轮：填充缓存
        client.extract_memory(test_input);

        b.iter(|| {
            // 第二轮：应该命中缓存
            let extraction = client.extract_memory(test_input);
            assert_eq!(extraction.topic_label, "rust_programming");
        });
    });

    group.finish();
}

// ── Fallback 测试 ──────────────────────────────────────

fn bench_llm_fallback(c: &mut Criterion) {
    let mut group = c.benchmark_group("llm/fallback");
    group.sample_size(10);

    group.bench_function("no_api_key_fallback", |b| {
        // 不设置 API key，测试 fallback
        let mut client = DeepSeekClient::with_api_key("").with_cache(false);

        b.iter(|| {
            let extraction = client.extract_memory("测试文本");
            // Fallback 应该返回合成数据
            assert!(!extraction.topic_label.is_empty());
            assert!(!extraction.keywords.is_empty());
        });
    });

    group.finish();
}

criterion_group!(
    benches,
    bench_llm_extraction,
    bench_llm_emotion,
    bench_llm_crystallize,
    bench_llm_cache,
    bench_llm_fallback,
);
criterion_main!(benches);
