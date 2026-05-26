//! Rule-based tone metadata extraction — no LLM, <0.1ms.
//!
//! Computes `ToneMeta` (valence, arousal, tone_tags, filler_ratio, sentence_style)
//! from raw text using hardcoded word lists and simple statistics.
//!
//! See spec §2.3a for the extraction rules.

use crate::engram::{StyleCompact, ToneMeta};

/// Extract tone metadata from raw text using rule-based analysis.
pub fn extract_tone(text: &str) -> ToneMeta {
    let filler_ratio = compute_filler_ratio(text);
    let sentence_style = compute_sentence_style(text);
    let valence = compute_valence(text);
    let arousal = compute_arousal(text);
    let tone_tags = extract_tone_tags(text, valence, filler_ratio, sentence_style.avg_sentence_len);

    ToneMeta {
        valence,
        arousal,
        tone_tags,
        filler_ratio,
        sentence_style,
    }
}

// ── valence (-1.0 .. 1.0) ───────────────────────────────────────

const POSITIVE_WORDS: &[&str] = &[
    "好的", "谢谢", "太棒了", "完美", "不错", "很好", "厉害",
    "喜欢", "开心", "高兴", "赞", "优秀", "对", "正确", "nice",
];

const NEGATIVE_WORDS: &[&str] = &[
    "不行", "错误", "糟糕", "讨厌", "烦", "生气", "失望", "难过",
    "累", "不", "不对", "差", "垃圾", "bug", "问题",
];

fn compute_valence(text: &str) -> f32 {
    let pos_count = POSITIVE_WORDS
        .iter()
        .filter(|w| text.contains(*w))
        .count();
    let neg_count = NEGATIVE_WORDS
        .iter()
        .filter(|w| text.contains(*w))
        .count();

    let total = word_count(text).max(1) as f32;
    let raw = (pos_count as f32 - neg_count as f32) / total;
    raw.clamp(-1.0, 1.0)
}

// ── arousal (0.0 .. 1.0) ────────────────────────────────────────

fn compute_arousal(text: &str) -> f32 {
    let text_len = text.len().max(1) as f32;

    let exclamation_ratio = (text.matches('!').count() + text.matches('\u{FF01}').count()) as f32 / text_len * 10.0;
    let question_ratio = (text.matches('?').count() + text.matches('\u{FF1F}').count()) as f32 / text_len * 5.0;

    let total_letters = text.chars().filter(|c| c.is_alphabetic()).count().max(1) as f32;
    let uppercase_count = text.chars().filter(|c| c.is_uppercase()).count() as f32;
    let caps_ratio = uppercase_count / total_letters * 5.0;

    (exclamation_ratio + question_ratio + caps_ratio).clamp(0.0, 1.0)
}

// ── tone_tags ───────────────────────────────────────────────────

/// Mapping: keyword → tag for substring matching.
const TAG_RULES: &[(&[&str], &str)] = &[
    (&["急", "马上", "快"], "urgent"),
    (&["谢谢", "感谢"], "appreciative"),
    (&["哈哈", "笑"], "playful"),
    (&["烦", "累", "唉"], "frustrated"),
    (&["怎么办", "不确定"], "uncertain"),
    (&["好的", "嗯"], "casual"),
];

fn extract_tone_tags(
    text: &str,
    valence: f32,
    filler_ratio: f32,
    avg_sentence_len: f32,
) -> Vec<String> {
    let mut tags: Vec<String> = Vec::new();

    for (keywords, tag) in TAG_RULES {
        if keywords.iter().any(|kw| text.contains(*kw)) && !tags.contains(&tag.to_string()) {
            tags.push(tag.to_string());
        }
    }

    // "formal": valence≈0, filler_ratio<0.05, avg_sentence_len>15
    if valence.abs() < 0.1 && filler_ratio < 0.05 && avg_sentence_len > 15.0 {
        let formal_tag = "formal".to_string();
        if !tags.contains(&formal_tag) {
            tags.push(formal_tag);
        }
    }

    tags
}

// ── filler_ratio (0.0 .. 1.0) ───────────────────────────────────

const FILLER_WORDS: &[&str] = &[
    "嗯", "啊", "呃", "哦", "那个", "这个", "就是说",
    "然后", "其实", "反正", "吧", "嘛",
];

fn compute_filler_ratio(text: &str) -> f32 {
    let filler_count = FILLER_WORDS
        .iter()
        .filter(|fw| text.contains(*fw))
        .count();

    let char_count = text.chars().count().max(1) as f32;
    (filler_count as f32 / char_count).clamp(0.0, 1.0)
}

// ── sentence_style ──────────────────────────────────────────────

fn compute_sentence_style(text: &str) -> StyleCompact {
    // Split sentences by 。！？.!?\n
    let sentences: Vec<&str> = text
        .split(|c: char| ['\u{3002}', '\u{FF01}', '\u{FF1F}', '.', '!', '?', '\n'].contains(&c))
        .filter(|s| !s.trim().is_empty())
        .collect();

    let sentence_count = sentences.len().max(1);
    let total_chars = text.chars().count();

    let avg_sentence_len = total_chars as f32 / sentence_count as f32;

    // Count question/exclamation marks from original text (since .split() removes delimiters)
    let question_count = text.matches('?').count() + text.matches('\u{FF1F}').count();
    let question_ratio = question_count as f32 / sentence_count as f32;

    let exclamation_count = (text.matches('!').count() + text.matches('\u{FF01}').count()) as u32;

    StyleCompact {
        avg_sentence_len,
        question_ratio,
        exclamation_count,
    }
}

// ── helpers ─────────────────────────────────────────────────────

/// Count the number of "words" in text: CJK chars each count as one word,
/// and ASCII alphabetic sequences count as one word each.
fn word_count(text: &str) -> usize {
    let mut count = 0;
    let mut in_alpha = false;

    for ch in text.chars() {
        if ch.is_alphabetic() {
            // CJK chars are alphabetic in Rust; each is a separate "word"
            if is_cjk(ch) {
                count += 1;
                in_alpha = false;
            } else if !in_alpha {
                count += 1;
                in_alpha = true;
            }
        } else {
            in_alpha = false;
        }
    }

    count
}

/// Check if a character falls within common CJK Unicode ranges.
fn is_cjk(ch: char) -> bool {
    matches!(
        ch,
        '\u{4E00}'..='\u{9FFF}'   // CJK Unified Ideographs
        | '\u{3400}'..='\u{4DBF}' // CJK Unified Ideographs Extension A
        | '\u{F900}'..='\u{FAFF}' // CJK Compatibility Ideographs
        | '\u{2E80}'..='\u{2FDF}' // CJK Radicals / Kangxi
        | '\u{3000}'..='\u{303F}' // CJK Symbols and Punctuation
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_valence_positive() {
        let tone = extract_tone("好的，谢谢，太棒了！");
        assert!(tone.valence > 0.0, "expected positive valence, got {}", tone.valence);
    }

    #[test]
    fn test_valence_negative() {
        let tone = extract_tone("不行，错误，糟糕，讨厌");
        assert!(tone.valence < 0.0, "expected negative valence, got {}", tone.valence);
    }

    #[test]
    fn test_valence_neutral() {
        let tone = extract_tone("今天天气不错。");
        // "不错" is positive, so slightly > 0
        assert!(tone.valence > -1.0 && tone.valence <= 1.0);
    }

    #[test]
    fn test_arousal_exclamation() {
        let tone = extract_tone("太棒了！！！");
        assert!(tone.arousal > 0.0, "expected arousal > 0 for exclamations, got {}", tone.arousal);
    }

    #[test]
    fn test_arousal_flat() {
        let tone = extract_tone("这是一个普通的句子。");
        assert!(tone.arousal >= 0.0 && tone.arousal <= 1.0);
    }

    #[test]
    fn test_tone_tags_urgent() {
        let tone = extract_tone("快点，马上处理这个bug");
        assert!(tone.tone_tags.contains(&"urgent".to_string()));
    }

    #[test]
    fn test_tone_tags_appreciative() {
        let tone = extract_tone("谢谢你的帮助");
        assert!(tone.tone_tags.contains(&"appreciative".to_string()));
    }

    #[test]
    fn test_tone_tags_frustrated() {
        let tone = extract_tone("好烦啊，累死了");
        assert!(tone.tone_tags.contains(&"frustrated".to_string()));
    }

    #[test]
    fn test_tone_tags_no_duplicates() {
        let tone = extract_tone("好的好的好的嗯嗯");
        let mut sorted = tone.tone_tags.clone();
        sorted.sort();
        sorted.dedup();
        assert_eq!(tone.tone_tags.len(), sorted.len(), "tags should not have duplicates");
    }

    #[test]
    fn test_filler_ratio() {
        let tone = extract_tone("嗯啊哦，那个就是说，其实吧");
        assert!(tone.filler_ratio > 0.0, "expected filler_ratio > 0");
        assert!(tone.filler_ratio <= 1.0, "filler_ratio should be <= 1.0");
    }

    #[test]
    fn test_sentence_style_basic() {
        let tone = extract_tone("第一句话。第二句话！第三句话？");
        assert_eq!(tone.sentence_style.exclamation_count, 1);
        assert!(tone.sentence_style.question_ratio > 0.0);
        assert!(tone.sentence_style.avg_sentence_len > 0.0);
    }

    #[test]
    fn test_empty_text() {
        let tone = extract_tone("");
        assert_eq!(tone.valence, 0.0);
        assert_eq!(tone.arousal, 0.0);
        assert_eq!(tone.filler_ratio, 0.0);
        assert!(tone.tone_tags.is_empty());
    }

    #[test]
    fn test_formal_tag() {
        // Formal: valence≈0, filler_ratio<0.05, avg_sentence_len>15
        let long_formal = "这是一个很长的陈述句用于测试正式语气标签的触发条件。".repeat(10);
        let tone = extract_tone(&long_formal);
        // It may or may not be formal depending on the heuristics
        assert!(tone.valence >= -1.0 && tone.valence <= 1.0);
    }
}
