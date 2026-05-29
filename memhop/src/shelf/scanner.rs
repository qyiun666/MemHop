//! Directory scanner — recursively walks a directory filtering by domain-appropriate extensions.
//!
//! v0.10.0: Extracted from the old `scan_and_chunk()` into its own module.
//! Supports single file and recursive directory scanning.

use crate::types::ShelfDomain;

/// A file discovered during scanning.
pub struct ScannedFile {
    pub path: String,
    pub text: String,
    pub domain: ShelfDomain,
}

/// Scan a path (file or directory) and return all matching files.
///
/// For a single file, returns it directly (ignoring extension filtering).
/// For a directory, walks recursively and filters by domain-appropriate extensions.
pub fn scan_directory(path: &str, domain: &ShelfDomain) -> Result<Vec<ScannedFile>, String> {
    let path_obj = std::path::Path::new(path);
    if path_obj.is_file() {
        let text =
            std::fs::read_to_string(path).map_err(|e| format!("Failed to read file {}: {}", path, e))?;
        return Ok(vec![ScannedFile {
            path: path.to_string(),
            text,
            domain: *domain,
        }]);
    }

    if path_obj.is_dir() {
        let mut files = Vec::new();
        scan_dir_recursive(path, domain, &mut files)?;
        return Ok(files);
    }

    Err(format!("Path is neither file nor directory: {}", path))
}

/// Recursively walk a directory, collecting files that match the domain's extensions.
fn scan_dir_recursive(
    dir: &str,
    domain: &ShelfDomain,
    files: &mut Vec<ScannedFile>,
) -> Result<(), String> {
    let extensions = domain_extensions(domain);

    let entries =
        std::fs::read_dir(dir).map_err(|e| format!("Failed to read dir {}: {}", dir, e))?;

    for entry in entries {
        let entry = entry.map_err(|e| format!("Dir entry error: {}", e))?;
        let entry_path = entry.path();

        if entry_path.is_dir() {
            // Skip hidden directories (e.g. .git, node_modules)
            let name = entry_path.file_name().unwrap_or_default();
            if name.to_str().is_none_or(|s| s.starts_with('.')) {
                continue;
            }
            let subdir = entry_path.to_string_lossy().to_string();
            scan_dir_recursive(&subdir, domain, files)?;
        } else if entry_path.is_file() {
            // Skip hidden files
            let name = entry_path.file_name().unwrap_or_default();
            if name.to_str().is_none_or(|s| s.starts_with('.')) {
                continue;
            }

            if let Some(ext) = entry_path.extension()
                && extensions.contains(&ext.to_str().unwrap_or(""))
                && let Ok(text) = std::fs::read_to_string(&entry_path)
            {
                let file_path = entry_path.to_string_lossy().to_string();
                files.push(ScannedFile {
                    path: file_path,
                    text,
                    domain: *domain,
                });
            }
        }
    }

    Ok(())
}

/// Return the list of allowed file extensions for a given domain.
fn domain_extensions(domain: &ShelfDomain) -> Vec<&'static str> {
    match domain {
        ShelfDomain::Code => vec![
            "rs", "py", "js", "ts", "go", "java", "c", "cpp", "h", "hpp", "rb", "php", "swift",
            "kt",
        ],
        ShelfDomain::Doc => vec!["md", "txt", "rst", "adoc"],
        ShelfDomain::Paper => vec!["md", "txt"],
        ShelfDomain::Custom => vec!["md", "txt", "json", "yaml", "yml", "toml"],
        ShelfDomain::Book => vec!["md", "txt"],
        ShelfDomain::Generic => vec!["md", "txt"],
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn create_temp_dir() -> (tempfile::TempDir, String) {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().to_string_lossy().to_string();
        (dir, path)
    }

    fn write_file(dir: &str, name: &str, content: &str) {
        let full_path = format!("{}/{}", dir, name);
        std::fs::write(&full_path, content).unwrap();
    }

    #[test]
    fn test_scan_single_file() {
        let (_dir, tmp) = create_temp_dir();
        write_file(&tmp, "test.rs", "fn main() {}");

        let file = format!("{}/test.rs", tmp);
        let results = scan_directory(&file, &ShelfDomain::Code).unwrap();
        assert_eq!(results.len(), 1);
        assert_eq!(results[0].text, "fn main() {}");
        assert!(results[0].path.ends_with("test.rs"));
    }

    #[test]
    fn test_scan_code_dir_filters_extensions() {
        let (_dir, tmp) = create_temp_dir();
        write_file(&tmp, "main.rs", "fn main() {}");
        write_file(&tmp, "lib.py", "print('hello')");
        write_file(&tmp, "readme.md", "# Readme");
        write_file(&tmp, "data.json", "{}");

        let results = scan_directory(&tmp, &ShelfDomain::Code).unwrap();
        // Should find .rs and .py, not .md or .json
        assert_eq!(results.len(), 2);
        let paths: Vec<&str> = results.iter().map(|f| {
            std::path::Path::new(&f.path)
                .file_name()
                .unwrap()
                .to_str()
                .unwrap()
        }).collect();
        assert!(paths.contains(&"main.rs"));
        assert!(paths.contains(&"lib.py"));
    }

    #[test]
    fn test_scan_recursive() {
        let (_dir, tmp) = create_temp_dir();
        let subdir = format!("{}/sub", tmp);
        std::fs::create_dir_all(&subdir).unwrap();
        write_file(&tmp, "main.rs", "fn main() {}");
        write_file(&subdir, "helper.rs", "fn helper() {}");

        let results = scan_directory(&tmp, &ShelfDomain::Code).unwrap();
        assert_eq!(results.len(), 2);
    }

    #[test]
    fn test_scan_skips_hidden_dirs() {
        let (_dir, tmp) = create_temp_dir();
        let hidden = format!("{}/.git", tmp);
        std::fs::create_dir_all(&hidden).unwrap();
        write_file(&hidden, "config", "some config");

        // Also a non-hidden file
        write_file(&tmp, "main.rs", "fn main() {}");

        let results = scan_directory(&tmp, &ShelfDomain::Code).unwrap();
        assert_eq!(results.len(), 1);
        assert!(results[0].path.ends_with("main.rs"));
    }

    #[test]
    fn test_scan_skips_hidden_files() {
        let (_dir, tmp) = create_temp_dir();
        write_file(&tmp, ".hidden.rs", "fn hidden() {}");
        write_file(&tmp, "visible.rs", "fn visible() {}");

        let results = scan_directory(&tmp, &ShelfDomain::Code).unwrap();
        assert_eq!(results.len(), 1);
        assert!(results[0].path.ends_with("visible.rs"));
    }

    #[test]
    fn test_scan_empty_dir() {
        let (_dir, tmp) = create_temp_dir();
        let results = scan_directory(&tmp, &ShelfDomain::Code).unwrap();
        assert!(results.is_empty());
    }

    #[test]
    fn test_scan_nonexistent_path() {
        let result = scan_directory("/nonexistent/path", &ShelfDomain::Code);
        assert!(result.is_err());
    }
}
