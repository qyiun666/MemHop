//! Semantic chunker — splits text into chunks based on content type.
//!
//! v0.10.0: Extracted from the old `mod.rs` private chunking functions into public API.
//! Each domain gets an appropriate chunking strategy:
//! - Code: line-based splitting at paragraph boundaries (50-200 line segments)
//! - Doc: markdown heading boundaries
//! - Paper/Book: paragraph (blank-line) boundaries
//! - Custom: fixed-token windows (~2048 chars / 512 tokens)

use crate::types::ChunkMeta;

/// Chunk source code files by line.
///
/// Strategy:
/// - Files ≤200 lines: one chunk.
/// - Larger files: split at blank-line paragraph boundaries.
///   Each segment is 50-200 lines (force-split at 200, never split below 50).
/// - Location format: `path:start_line-end_line`
pub fn chunk_code(path: &str, text: &str) -> Vec<(String, ChunkMeta)> {
    let source = path.to_string();
    let lines: Vec<&str> = text.lines().collect();
    let total_lines = lines.len();

    if total_lines == 0 {
        return Vec::new();
    }

    // Small file: whole file as one chunk
    if total_lines <= 200 {
        return vec![(
            text.to_string(),
            ChunkMeta {
                source,
                location: format!("{}:1-{}", path, total_lines),
                url: None,
            },
        )];
    }

    let mut chunks = Vec::new();
    let mut start = 0;

    for i in 0..total_lines {
        let segment_len = i.saturating_sub(start);

        // Force split if segment reaches 200 lines
        if segment_len >= 200 {
            let chunk_text = lines[start..i].join("\n");
            chunks.push((
                chunk_text,
                ChunkMeta {
                    source: source.clone(),
                    location: format!("{}:{}-{}", path, start + 1, i),
                    url: None,
                },
            ));
            start = i;
            continue;
        }

        // Split at blank line boundary when segment is at least 50 lines
        if segment_len >= 50 && lines[i].trim().is_empty() {
            let chunk_text = lines[start..i].join("\n");
            chunks.push((
                chunk_text,
                ChunkMeta {
                    source: source.clone(),
                    location: format!("{}:{}-{}", path, start + 1, i),
                    url: None,
                },
            ));
            start = i + 1; // skip blank line
        }
    }

    // Last chunk
    if start < total_lines {
        let chunk_text = lines[start..].join("\n");
        chunks.push((
            chunk_text,
            ChunkMeta {
                source,
                location: format!("{}:{}-{}", path, start + 1, total_lines),
                url: None,
            },
        ));
    }

    chunks
}

/// Chunk documentation text by markdown headings.
///
/// Each markdown heading (`# Heading`) starts a new chunk.
/// Location is set to the heading text.
pub fn chunk_doc(text: &str) -> Vec<(String, ChunkMeta)> {
    let mut chunks = Vec::new();
    let mut current_section = String::new();
    let mut current_heading = "top".to_string();

    for line in text.lines() {
        let trimmed = line.trim();
        if trimmed.starts_with('#') && trimmed.len() > 1 {
            // Save previous section
            if !current_section.is_empty() {
                chunks.push((
                    current_section.clone(),
                    ChunkMeta {
                        source: String::new(),
                        location: current_heading.clone(),
                        url: None,
                    },
                ));
                current_section.clear();
            }
            current_heading = trimmed.to_string();
        } else {
            if !current_section.is_empty() {
                current_section.push('\n');
            }
            current_section.push_str(line);
        }
    }

    // Last section
    if !current_section.is_empty() {
        chunks.push((
            current_section,
            ChunkMeta {
                source: String::new(),
                location: current_heading,
                url: None,
            },
        ));
    }

    chunks
}

/// Chunk paper/book text by paragraph (blank-line separated).
///
/// Each paragraph becomes one chunk.
/// Location is set to `paragraph_N`.
pub fn chunk_paper(text: &str) -> Vec<(String, ChunkMeta)> {
    let mut chunks = Vec::new();
    let mut current = String::new();
    let mut para_idx = 0;

    for line in text.lines() {
        if line.trim().is_empty() {
            if !current.is_empty() {
                chunks.push((
                    current.clone(),
                    ChunkMeta {
                        source: String::new(),
                        location: format!("paragraph_{}", para_idx),
                        url: None,
                    },
                ));
                current.clear();
                para_idx += 1;
            }
        } else {
            if !current.is_empty() {
                current.push(' ');
            }
            current.push_str(line.trim());
        }
    }

    if !current.is_empty() {
        chunks.push((
            current,
            ChunkMeta {
                source: String::new(),
                location: format!("paragraph_{}", para_idx),
                url: None,
            },
        ));
    }

    chunks
}

/// Chunk custom domain text by fixed token window (~4 chars/token).
///
/// Each chunk is approximately 512 tokens (~2048 characters).
/// Location is set to `chunk_N`.
pub fn chunk_custom(text: &str) -> Vec<(String, ChunkMeta)> {
    let char_limit = 512 * 4;
    let mut chunks = Vec::new();
    let mut chunk_idx = 0;
    let mut pos = 0;

    while pos < text.len() {
        let end = (pos + char_limit).min(text.len());
        let chunk = &text[pos..end];
        chunks.push((
            chunk.to_string(),
            ChunkMeta {
                source: String::new(),
                location: format!("chunk_{}", chunk_idx),
                url: None,
            },
        ));
        pos = end;
        chunk_idx += 1;
    }

    chunks
}

#[cfg(test)]
mod tests {
    use super::*;

    // ── chunk_code tests ──────────────────────────────────────

    #[test]
    fn test_chunk_code_empty() {
        let chunks = chunk_code("test.rs", "");
        assert!(chunks.is_empty());
    }

    #[test]
    fn test_chunk_code_small_file() {
        let text = "line1\nline2\nline3";
        let chunks = chunk_code("test.rs", text);
        assert_eq!(chunks.len(), 1);
        assert_eq!(chunks[0].0, text);
        assert_eq!(chunks[0].1.location, "test.rs:1-3");
    }

    #[test]
    fn test_chunk_code_splits_at_blank_lines() {
        // 250 lines with blank lines at strategic positions
        let mut lines = Vec::new();
        for i in 0..100 {
            lines.push(format!("line {}", i));
        }
        lines.push(String::new()); // blank line at index 100 → segment_len=100 ≥ 50 → split
        for i in 0..149 {
            lines.push(format!("line {}", 100 + i));
        }
        let text = lines.join("\n");
        let chunks = chunk_code("test.rs", &text);
        assert!(
            chunks.len() >= 2,
            "Expected at least 2 chunks for 250-line file with blank line, got {}",
            chunks.len()
        );
        assert!(
            chunks[0].1.location.starts_with("test.rs:"),
            "Location should start with path, got: {}",
            chunks[0].1.location
        );
    }

    #[test]
    fn test_chunk_code_force_split_at_200() {
        // 250 lines with no blank lines → force split at 200
        let lines: Vec<String> = (0..250).map(|i| format!("line {}", i)).collect();
        let text = lines.join("\n");
        let chunks = chunk_code("test.rs", &text);
        assert!(chunks.len() >= 2, "Expected >=2 chunks, got {}", chunks.len());
        // First chunk should be ~200 lines
        assert!(
            chunks[0].1.location.contains(":1-200")
                || chunks[0].1.location.contains(":1-201")
                || chunks[0].1.location.contains(":1-199"),
            "First chunk location unexpected: {}",
            chunks[0].1.location
        );
    }

    #[test]
    fn test_chunk_code_200_line_file_one_chunk() {
        let lines: Vec<String> = (0..200).map(|i| format!("line {}", i)).collect();
        let text = lines.join("\n");
        let chunks = chunk_code("test.rs", &text);
        assert_eq!(chunks.len(), 1);
    }

    // ── chunk_doc tests ───────────────────────────────────────

    #[test]
    fn test_chunk_doc_empty() {
        let chunks = chunk_doc("");
        assert!(chunks.is_empty());
    }

    #[test]
    fn test_chunk_doc_single_heading() {
        let text = "# Title\ncontent here";
        let chunks = chunk_doc(text);
        assert_eq!(chunks.len(), 1);
        assert!(chunks[0].0.contains("content here"));
        assert_eq!(chunks[0].1.location, "# Title");
    }

    #[test]
    fn test_chunk_doc_multiple_headings() {
        let text = "# Title\nintro\n## Section 1\nbody1\n## Section 2\nbody2\n";
        let chunks = chunk_doc(text);
        assert_eq!(chunks.len(), 3);
        assert_eq!(chunks[0].1.location, "# Title");
        assert_eq!(chunks[1].1.location, "## Section 1");
        assert_eq!(chunks[2].1.location, "## Section 2");
    }

    #[test]
    fn test_chunk_doc_no_heading() {
        let text = "plain text\nno headings\nhere";
        let chunks = chunk_doc(text);
        assert_eq!(chunks.len(), 1);
        assert_eq!(chunks[0].1.location, "top");
    }

    // ── chunk_paper tests ─────────────────────────────────────

    #[test]
    fn test_chunk_paper_empty() {
        let chunks = chunk_paper("");
        assert!(chunks.is_empty());
    }

    #[test]
    fn test_chunk_paper_single_paragraph() {
        let text = "This is a single paragraph across multiple\nlines without blank separator.";
        let chunks = chunk_paper(text);
        assert_eq!(chunks.len(), 1);
        assert!(chunks[0].0.contains("single paragraph"));
    }

    #[test]
    fn test_chunk_paper_multiple_paragraphs() {
        let text = "First paragraph.\n\nSecond paragraph.\n\nThird paragraph.";
        let chunks = chunk_paper(text);
        assert_eq!(chunks.len(), 3);
        assert_eq!(chunks[0].1.location, "paragraph_0");
        assert_eq!(chunks[1].1.location, "paragraph_1");
        assert_eq!(chunks[2].1.location, "paragraph_2");
    }

    // ── chunk_custom tests ────────────────────────────────────

    #[test]
    fn test_chunk_custom_empty() {
        let chunks = chunk_custom("");
        assert!(chunks.is_empty());
    }

    #[test]
    fn test_chunk_custom_small() {
        let text = "short text";
        let chunks = chunk_custom(text);
        assert_eq!(chunks.len(), 1);
        assert_eq!(chunks[0].0, text);
        assert_eq!(chunks[0].1.location, "chunk_0");
    }

    #[test]
    fn test_chunk_custom_large() {
        // ~3000 chars should produce 2 chunks (2048 char each)
        let text = "x".repeat(3000);
        let chunks = chunk_custom(&text);
        assert_eq!(chunks.len(), 2);
        assert_eq!(chunks[0].1.location, "chunk_0");
        assert_eq!(chunks[1].1.location, "chunk_1");
    }
}
