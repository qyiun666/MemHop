//! Shelf scanner — recursive directory scanning with domain-aware file filtering.

use std::fs;
use std::path::Path;

use crate::types::ShelfDomain;

/// A scanned file with its path and content.
pub struct ScannedFile {
    pub path: String,
    pub content: String,
}

/// Extension filter per domain.
fn allowed_extensions(domain: &ShelfDomain) -> &'static [&'static str] {
    match domain {
        ShelfDomain::Code => &[
            "rs", "py", "js", "ts", "go", "java", "c", "cpp", "h", "hpp", "rb", "swift", "kt",
        ],
        ShelfDomain::Doc => &["md", "txt", "rst", "adoc", "markdown"],
        ShelfDomain::Book => &["md", "txt"],
        ShelfDomain::Paper => &["md", "txt"],
        ShelfDomain::Generic => &["md", "txt", "rs", "py", "js", "toml", "json", "yaml", "yml"],
    }
}

/// Recursively scan a directory, returning files filtered by domain.
pub fn scan(dir_path: &str, domain: &ShelfDomain) -> Result<Vec<ScannedFile>, String> {
    let path = Path::new(dir_path);
    if !path.exists() {
        return Err(format!("path does not exist: {}", dir_path));
    }
    if !path.is_dir() {
        return Err(format!("path is not a directory: {}", dir_path));
    }

    let extensions = allowed_extensions(domain);
    let mut results = Vec::new();
    scan_recursive(path, extensions, &mut results)?;
    Ok(results)
}

fn scan_recursive(
    dir: &Path,
    extensions: &[&str],
    results: &mut Vec<ScannedFile>,
) -> Result<(), String> {
    let entries = fs::read_dir(dir).map_err(|e| format!("read_dir {}: {}", dir.display(), e))?;

    for entry in entries {
        let entry = entry.map_err(|e| format!("entry: {}", e))?;
        let path = entry.path();

        if path.is_dir() {
            // Skip hidden directories (starting with '.')
            if let Some(name) = path.file_name().and_then(|n| n.to_str())
                && !name.starts_with('.')
            {
                scan_recursive(&path, extensions, results)?;
            }
        } else if path.is_file()
            && let Some(ext) = path.extension().and_then(|e| e.to_str())
        {
            let ext_lower = ext.to_lowercase();
            if extensions.contains(&ext_lower.as_str()) {
                let content = fs::read_to_string(&path)
                    .map_err(|e| format!("read {}: {}", path.display(), e))?;
                if !content.trim().is_empty() {
                    results.push(ScannedFile {
                        path: path.to_string_lossy().to_string(),
                        content,
                    });
                }
            }
        }
    }

    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_allowed_extensions_code() {
        let exts = allowed_extensions(&ShelfDomain::Code);
        assert!(exts.contains(&"rs"));
        assert!(exts.contains(&"py"));
        assert!(!exts.contains(&"md"));
    }

    #[test]
    fn test_scan_non_existent_dir() {
        let result = scan("/tmp/nonexistent_dir_abcdef", &ShelfDomain::Generic);
        assert!(result.is_err());
    }
}
