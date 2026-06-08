//! splitter — long text splitting for batch retrieval.
//! Splits long input by paragraph boundaries, fallback to sentence split.

const DEFAULT_MAX_CHUNK: usize = 512;

/// Split text into chunks by paragraph boundaries (`\n\n`), then by sentence if needed.
/// Returns a list of non-empty chunks.
pub fn split_text(text: &str, max_len: usize) -> Vec<String> {
    let max = if max_len == 0 {
        DEFAULT_MAX_CHUNK
    } else {
        max_len
    };

    if text.is_empty() {
        return Vec::new();
    }

    if text.chars().count() <= max {
        return vec![text.to_string()];
    }

    let mut chunks = Vec::new();

    // First pass: split by double newline (paragraph boundary)
    let paragraphs: Vec<&str> = text.split("\n\n").collect();
    let mut current_chunk = String::new();

    for para in paragraphs {
        let para = para.trim();
        if para.is_empty() {
            continue;
        }

        if current_chunk.is_empty() {
            if para.chars().count() <= max {
                current_chunk = para.to_string();
            } else {
                // Paragraph too long, split by sentence
                for sent in split_sentences(para, max) {
                    chunks.push(sent);
                }
            }
        } else {
            let combined_len = current_chunk.chars().count() + 2 + para.chars().count();
            if combined_len <= max {
                current_chunk.push_str("\n\n");
                current_chunk.push_str(para);
            } else {
                // Flush current chunk
                chunks.push(current_chunk.clone());
                current_chunk.clear();
                if para.chars().count() <= max {
                    current_chunk = para.to_string();
                } else {
                    for sent in split_sentences(para, max) {
                        chunks.push(sent);
                    }
                }
            }
        }
    }

    if !current_chunk.is_empty() {
        chunks.push(current_chunk);
    }

    chunks
}

/// Split a long paragraph into sentences, respecting max_len.
/// Preserves original sentence-ending punctuation.
fn split_sentences(text: &str, max_len: usize) -> Vec<String> {
    let mut result = Vec::new();
    // Use split_inclusive to preserve delimiters
    let sentences: Vec<&str> = text
        .split_inclusive(|c: char| {
            c == '。' || c == '！' || c == '？' || c == '.' || c == '!' || c == '?'
        })
        .collect();

    let mut current = String::new();
    for sent in sentences {
        let sent = sent.trim();
        if sent.is_empty() {
            continue;
        }

        if current.is_empty() {
            if sent.chars().count() <= max_len {
                current = sent.to_string();
            } else {
                result.extend(hard_split(sent, max_len));
            }
        } else {
            let combined = current.chars().count() + sent.chars().count();
            if combined <= max_len {
                current.push_str(sent);
            } else {
                result.push(current.clone());
                current.clear();
                if sent.chars().count() <= max_len {
                    current = sent.to_string();
                } else {
                    result.extend(hard_split(sent, max_len));
                }
            }
        }
    }
    if !current.is_empty() {
        result.push(current);
    }
    result
}

/// Hard split by character count when no natural boundaries found.
fn hard_split(text: &str, max_len: usize) -> Vec<String> {
    let chars: Vec<char> = text.chars().collect();
    let mut result = Vec::new();
    let mut start = 0;
    while start < chars.len() {
        let end = (start + max_len).min(chars.len());
        result.push(chars[start..end].iter().collect());
        start = end;
    }
    result
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_paragraph_split() {
        let text = "Para one.\n\nPara two.\n\nPara three.";
        let chunks = split_text(text, 12);
        assert_eq!(chunks.len(), 3);
    }

    #[test]
    fn test_chinese_split() {
        let text = "这是第一段。\n\n这是第二段。内容比较多。\n\n这是第三段。";
        let chunks = split_text(text, 15);
        assert!(chunks.len() >= 2);
    }
}
