//! shelf summarizer — 领域策略骨架提取器。
//!
//! 为每种 ShelfDomain 提取文本的"骨架"（函数签名/章节标题/段落首句等），
//! 用于 L3 主节点优先检索的结构节点。
//!
//! 纯启发式实现，无外部 NLP 依赖。

use crate::types::{ShelfDomain, SourceKind, SourceRef};

/// 骨架摘要结果
pub struct SkeletalSummary {
    /// 全文的骨架化文本（可用于检索展示）
    pub skeletal_text: String,
    /// 结构节点列表（每个节点对应一个 is_structural=true 的 KnowledgeNode）
    pub structural_nodes: Vec<StructuralChunk>,
}

/// 单块结构节点
pub struct StructuralChunk {
    pub text: String,
    /// 指向原始文本中的位置
    pub source_ref: SourceRef,
    /// 片段序号
    pub order: usize,
}

/// 入口函数：按领域策略提取骨架
pub fn summarize(content: &str, domain: &ShelfDomain) -> SkeletalSummary {
    match domain {
        ShelfDomain::Code => summarize_code(content),
        ShelfDomain::Doc => summarize_doc(content),
        ShelfDomain::Book => summarize_book(content),
        ShelfDomain::Paper => summarize_paper(content),
        ShelfDomain::Generic => summarize_generic(content),
    }
}

/// Code 领域：提取函数/结构体/trait/模块签名
fn summarize_code(content: &str) -> SkeletalSummary {
    let lines: Vec<&str> = content.lines().collect();
    let mut nodes = Vec::new();
    let mut skeletal_parts: Vec<String> = Vec::new();

    // 匹配常见编程语言签名行
    let signatures = [
        "fn ", "pub fn", "pub(crate) fn", "pub unsafe fn",
        "struct ", "pub struct", "enum ", "pub enum",
        "trait ", "pub trait", "impl ", "pub impl",
        "class ", "def ", "function ", "async fn",
        "pub async fn", "interface ", "type ", "pub type",
        "const ", "pub const", "static ", "pub static",
        "mod ", "pub mod",
    ];

    for (i, line) in lines.iter().enumerate() {
        let trimmed = line.trim_start();
        let leading_len = line.len() - trimmed.len();
        for sig in &signatures {
            if trimmed.starts_with(sig) && !trimmed.starts_with("//") && !trimmed.starts_with("#") {
                let text = trimmed.to_string();
                skeletal_parts.push(text.clone());
                nodes.push(StructuralChunk {
                    text,
                    source_ref: SourceRef {
                        kind: SourceKind::File,
                        location: String::new(),  // 调用方填充
                        line_range: Some((i + 1, i + 1)),
                        selector: None,
                        content_hash: None,
                    },
                    order: nodes.len(),
                });
                break;
            }
        }
        // 忽略前导空白计算
        let _ = leading_len;
    }

    SkeletalSummary {
        skeletal_text: skeletal_parts.join("\n"),
        structural_nodes: nodes,
    }
}

/// Doc 领域：提取 Markdown heading + 首段落
fn summarize_doc(content: &str) -> SkeletalSummary {
    let mut nodes = Vec::new();
    let mut skeletal_parts: Vec<String> = Vec::new();
    let lines: Vec<&str> = content.lines().collect();
    let mut i = 0;

    while i < lines.len() {
        let line = lines[i].trim();
        if line.starts_with('#') {
            // 提取 heading
            let heading = line.to_string();
            skeletal_parts.push(heading.clone());
            let start_line = i + 1;
            
            // 提取 heading 后的第一个非空段落首句
            let mut j = i + 1;
            while j < lines.len() {
                let l = lines[j].trim();
                if l.starts_with('#') {
                    break;
                }
                if !l.is_empty() {
                    let first_sentence = l.split(&['.', '!', '?'][..])
                        .next()
                        .unwrap_or(l)
                        .to_string();
                    skeletal_parts.push(format!("  {}", first_sentence));
                    break;
                }
                j += 1;
            }

            nodes.push(StructuralChunk {
                text: heading,
                source_ref: SourceRef {
                    kind: SourceKind::File,
                    location: String::new(),
                    line_range: Some((start_line, start_line)),
                    selector: None,
                    content_hash: None,
                },
                order: nodes.len(),
            });
        }
        i += 1;
    }

    SkeletalSummary {
        skeletal_text: skeletal_parts.join("\n"),
        structural_nodes: nodes,
    }
}

/// Book 领域：提取章节标题 + 每段首句
#[allow(unused_assignments)] // Variables are used in loop iterations
fn summarize_book(content: &str) -> SkeletalSummary {
    let mut nodes = Vec::new();
    let mut skeletal_parts: Vec<String> = Vec::new();
    let lines: Vec<&str> = content.lines().collect();
    let mut current_heading = String::new();
    let mut current_heading_line: usize = 0;

    for (i, line) in lines.iter().enumerate() {
        let trimmed = line.trim();
        if trimmed.starts_with('#') {
            current_heading = trimmed.to_string();
            current_heading_line = i + 1;
            skeletal_parts.push(format!("\n{}", current_heading));
            nodes.push(StructuralChunk {
                text: current_heading.clone(),
                source_ref: SourceRef {
                    kind: SourceKind::File,
                    location: String::new(),
                    line_range: Some((current_heading_line, current_heading_line)),
                    selector: None,
                    content_hash: None,
                },
                order: nodes.len(),
            });
        } else if trimmed.is_empty() {
            // 段落分隔符，取下一非空行首句
            if i + 1 < lines.len() {
                let next = lines[i + 1].trim();
                if !next.is_empty() && !next.starts_with('#') {
                    let first_sentence = next.split(&['.', '!', '?'][..])
                        .next()
                        .unwrap_or(next)
                        .to_string();
                    if first_sentence.len() > 10 {
                        skeletal_parts.push(format!("  {}...", first_sentence));
                    }
                }
            }
        }
    }

    SkeletalSummary {
        skeletal_text: skeletal_parts.join("\n"),
        structural_nodes: nodes,
    }
}

/// Paper 领域：提取 Abstract + Section heading + 结论
fn summarize_paper(content: &str) -> SkeletalSummary {
    let mut nodes = Vec::new();
    let mut skeletal_parts: Vec<String> = Vec::new();
    let lines: Vec<&str> = content.lines().collect();
    let mut in_abstract = false;
    let mut abstract_text = String::new();

    for (i, line) in lines.iter().enumerate() {
        let trimmed = line.trim().to_lowercase();

        // Abstract
        if trimmed.contains("abstract") && trimmed.len() < 20 {
            in_abstract = true;
            continue;
        }
        if in_abstract {
            if trimmed.starts_with('#') || trimmed.starts_with("1.") || trimmed.starts_with("introduction") {
                in_abstract = false;
                if !abstract_text.is_empty() {
                    let text = format!("Abstract: {}", &abstract_text[..abstract_text.len().min(200)]);
                    skeletal_parts.push(text.clone());
                    nodes.push(StructuralChunk {
                        text,
                        source_ref: SourceRef {
                            kind: SourceKind::File,
                            location: String::new(),
                            line_range: None,
                            selector: None,
                            content_hash: None,
                        },
                        order: nodes.len(),
                    });
                }
            } else if !trimmed.is_empty() {
                abstract_text.push_str(line.trim());
                abstract_text.push(' ');
            }
        }

        // Section heading
        let upper = line.trim().to_uppercase();
        let section_keywords = ["INTRODUCTION", "METHOD", "RESULT", "CONCLUSION", 
                                 "DISCUSSION", "EXPERIMENT", "EVALUATION", "RELATED WORK",
                                 "APPROACH", "IMPLEMENTATION"];
        if upper.len() < 50 && section_keywords.iter().any(|k| upper.contains(k)) {
            let text = line.trim().to_string();
            skeletal_parts.push(text.clone());
            nodes.push(StructuralChunk {
                text,
                source_ref: SourceRef {
                    kind: SourceKind::File,
                    location: String::new(),
                    line_range: Some((i + 1, i + 1)),
                    selector: None,
                    content_hash: None,
                },
                order: nodes.len(),
            });
        }
    }

    SkeletalSummary {
        skeletal_text: skeletal_parts.join("\n"),
        structural_nodes: nodes,
    }
}

/// Generic 领域：提取每段首句
fn summarize_generic(content: &str) -> SkeletalSummary {
    let mut nodes = Vec::new();
    let mut skeletal_parts: Vec<String> = Vec::new();
    // 按 \n\n 分段落
    let paragraphs: Vec<&str> = content.split("\n\n").collect();
    let mut char_offset = 0;
    let _lines_before: Vec<&str> = content.lines().collect();

    for para in &paragraphs {
        let trimmed = para.trim();
        if trimmed.is_empty() {
            char_offset += para.len() + 2; // +2 for "\n\n"
            continue;
        }
        // 取该段的第一句
        let first_sentence = trimmed.split(&['.', '!', '?'][..])
            .next()
            .unwrap_or(trimmed)
            .to_string();
        if first_sentence.len() < 5 {
            char_offset += para.len() + 2;
            continue;
        }
        // 估算所在行号
        let line_num = content[..char_offset.min(content.len())].matches('\n').count() + 1;
        skeletal_parts.push(first_sentence.clone());

        nodes.push(StructuralChunk {
            text: first_sentence,
            source_ref: SourceRef {
                kind: SourceKind::File,
                location: String::new(),
                line_range: Some((line_num, line_num)),
                selector: None,
                content_hash: None,
            },
            order: nodes.len(),
        });

        char_offset += para.len() + 2;
    }

    SkeletalSummary {
        skeletal_text: skeletal_parts.join("\n"),
        structural_nodes: nodes,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_summarize_code_fn_signatures() {
        let code = r#"fn hello() {
    println!("hello");
}

pub fn add(a: i32, b: i32) -> i32 {
    a + b
}

struct Point {
    x: f64,
    y: f64,
}

// comment
fn hidden() {}
"#;
        let result = summarize(code, &ShelfDomain::Code);
        assert!(!result.structural_nodes.is_empty(), "should extract signatures");
        assert!(result.structural_nodes.iter().any(|n| n.text.contains("fn hello")), "should find fn hello");
        assert!(result.structural_nodes.iter().any(|n| n.text.contains("pub fn add")), "should find pub fn add");
        assert!(result.structural_nodes.iter().any(|n| n.text.contains("struct Point")), "should find struct Point");
        assert!(result.structural_nodes.iter().any(|n| n.text.contains("fn hidden")), "should find fn hidden");
        // 不提取注释
        for n in &result.structural_nodes {
            assert!(!n.text.starts_with("//"), "should not extract comments");
        }
    }

    #[test]
    fn test_summarize_doc_headings() {
        let doc = r#"# Introduction
This is the intro paragraph.

## Setup
First, install the package.

## Usage
Run the command.
"#;
        let result = summarize(doc, &ShelfDomain::Doc);
        assert_eq!(result.structural_nodes.len(), 3, "should extract 3 headings");
        assert!(result.structural_nodes[0].text.contains("Introduction"));
        assert!(result.structural_nodes[1].text.contains("Setup"));
        assert!(result.structural_nodes[2].text.contains("Usage"));
        assert!(result.skeletal_text.contains("  This is the intro paragraph"), "should include first sentence after heading");
    }

    #[test]
    fn test_summarize_book_chapters() {
        let book = r#"# Chapter 1: The Beginning
It was a dark and stormy night. The wind howled.

The captain stood on the deck.
Another paragraph follows.

# Chapter 2: The Storm
The storm grew stronger.
"#;
        let result = summarize(book, &ShelfDomain::Book);
        assert_eq!(result.structural_nodes.len(), 2, "should extract 2 chapter headings");
        assert!(result.structural_nodes[0].text.contains("Chapter 1"));
        assert!(result.structural_nodes[1].text.contains("Chapter 2"));
        assert!(result.skeletal_text.contains("The Beginning"), "should include heading");
    }

    #[test]
    fn test_summarize_paper_sections() {
        let paper = r#"Abstract
We propose a novel method for memory recall.
Our approach achieves state-of-the-art results.

1. Introduction
Memory is fundamental to intelligence.

2. Method
We use a hypergraph structure.

3. Results
Our method outperforms baselines.

4. Conclusion
We presented a new approach.
"#;
        let result = summarize(paper, &ShelfDomain::Paper);
        assert!(result.skeletal_text.contains("Abstract"), "should include abstract");
        assert!(result.skeletal_text.contains("Introduction"), "should include introduction");
        assert!(result.skeletal_text.contains("Conclusion"), "should include conclusion");
    }

    #[test]
    fn test_summarize_generic_paragraphs() {
        let text = r#"This is the first paragraph about memory systems.
It has multiple sentences. But we only take the first one.

This is the second paragraph. It also has details.

Third paragraph here.
"#;
        let result = summarize(text, &ShelfDomain::Generic);
        assert!(result.structural_nodes.len() >= 3, "should extract first sentence from each paragraph");
        let sentences: Vec<&str> = result.structural_nodes.iter().map(|n| n.text.as_str()).collect();
        assert!(sentences.iter().any(|s| s.contains("first paragraph")));
        assert!(sentences.iter().any(|s| s.contains("second paragraph")));
        assert!(sentences.iter().any(|s| s.contains("Third paragraph")));
    }
}
