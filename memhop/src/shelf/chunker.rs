//! Shelf chunker — domain-specific text chunking strategies.
//! Heuristic-only: no external dependencies, no tree-sitter.

use crate::types::ShelfDomain;

/// Chunk a file's content based on domain strategy.
/// Returns a list of text chunks.
pub fn chunk(content: &str, domain: &ShelfDomain, max_size: usize) -> Vec<String> {
    match domain {
        ShelfDomain::Code => chunk_code(content, max_size),
        ShelfDomain::Doc => chunk_doc(content, max_size),
        ShelfDomain::Book => chunk_book(content, max_size),
        ShelfDomain::Paper => chunk_paper(content, max_size),
        ShelfDomain::Generic => chunk_generic(content, max_size),
    }
}

/// Code: split by 2+ consecutive blank lines (function/class boundaries).
fn chunk_code(content: &str, max_size: usize) -> Vec<String> {
    let mut chunks = Vec::new();
    let mut current = String::new();

    for line in content.lines() {
        let trimmed = line.trim();
        // 2+ consecutive blank lines signal a boundary
        if trimmed.is_empty() && current.ends_with("\n\n") {
            if !current.trim().is_empty() {
                if current.len() > max_size {
                    // Oversized chunk: split by single lines
                    chunks.extend(chunk_generic(&current, max_size));
                } else {
                    chunks.push(current.trim().to_string());
                }
                current = String::new();
            }
            continue;
        }

        if current.len() + line.len() + 1 > max_size && !current.is_empty() {
            chunks.push(current.trim().to_string());
            current = String::new();
        }
        current.push_str(line);
        current.push('\n');
    }

    if !current.trim().is_empty() {
        if current.len() > max_size {
            chunks.extend(chunk_generic(&current, max_size));
        } else {
            chunks.push(current.trim().to_string());
        }
    }

    chunks.into_iter().filter(|c| !c.is_empty()).collect()
}

/// Doc: split by Markdown headings (# or ##).
fn chunk_doc(content: &str, max_size: usize) -> Vec<String> {
    let mut chunks = Vec::new();
    let mut current = String::new();

    for line in content.lines() {
        let trimmed = line.trim();
        // New heading starts a new chunk
        if (trimmed.starts_with("# ") || trimmed.starts_with("## ")) && !current.is_empty() {
            if !current.trim().is_empty() {
                chunks.push(current.trim().to_string());
            }
            current = String::new();
        }

        if current.len() + line.len() + 1 > max_size && !current.is_empty() {
            chunks.push(current.trim().to_string());
            current = line.to_string();
            current.push('\n');
        } else {
            current.push_str(line);
            current.push('\n');
        }
    }

    if !current.trim().is_empty() {
        chunks.push(current.trim().to_string());
    }

    chunks
}

/// Book/Paper: split by paragraph boundaries (\n\n).
fn chunk_book(content: &str, max_size: usize) -> Vec<String> {
    let mut chunks = Vec::new();
    let mut current = String::new();

    for paragraph in content.split("\n\n") {
        let para = paragraph.trim();
        if para.is_empty() {
            continue;
        }

        if current.len() + para.len() + 2 > max_size && !current.is_empty() {
            chunks.push(current.trim().to_string());
            current = String::new();
        }
        current.push_str(para);
        current.push_str("\n\n");
    }

    if !current.trim().is_empty() {
        chunks.push(current.trim().to_string());
    }

    chunks
}

fn chunk_paper(content: &str, max_size: usize) -> Vec<String> {
    chunk_book(content, max_size)
}

/// Generic: split by fixed character count, preferring sentence/line boundaries.
fn chunk_generic(content: &str, max_size: usize) -> Vec<String> {
    let mut chunks = Vec::new();
    let mut current = String::new();

    for line in content.lines() {
        if current.len() + line.len() + 1 > max_size && !current.is_empty() {
            chunks.push(current.trim().to_string());
            current = String::new();
        }
        current.push_str(line);
        current.push('\n');
    }

    if !current.trim().is_empty() {
        chunks.push(current.trim().to_string());
    }

    chunks
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_chunk_code_double_newline() {
        let code = "fn foo() { return 1; }\n\n\nfn bar() { return 2; }";
        let result = chunk(code, &ShelfDomain::Code, 1000);
        assert_eq!(result.len(), 2, "3 newlines = 2 consecutive blank lines");
    }

    #[test]
    fn test_chunk_doc_headings() {
        let doc = "# Title\ncontent\n## Section 1\nsection content\n## Section 2\nmore content";
        let result = chunk(doc, &ShelfDomain::Doc, 1000);
        assert_eq!(result.len(), 3);
        assert!(result[0].contains("Title"));
        assert!(result[1].contains("Section 1"));
        assert!(result[2].contains("Section 2"));
    }
}
