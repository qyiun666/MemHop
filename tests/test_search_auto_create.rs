//! End-to-end tests for Interface 2: Search Memory
//!
//! Tests the search_memory() API with focus on auto_create functionality.

use memhop::query::types::SearchQuery;
use memhop::{MemHop, MemHopConfig};
use tempfile::TempDir;

/// Helper: Create a new MemHop database instance
fn create_test_db(name: &str) -> (TempDir, MemHop) {
    let temp_dir = TempDir::new().unwrap();
    let db_path = temp_dir.path().join(format!("{}.meh", name));
    let config = MemHopConfig::new(db_path.clone(), 768);
    let db = MemHop::open(config).unwrap();
    (temp_dir, db)
}

/// Helper: Create a search query with auto_create=0
fn search_query_no_create(dialogue: &str) -> SearchQuery {
    SearchQuery {
        dialogue: dialogue.to_string(),
        context_id: None,
        l3_id: None,
        context_limit: 10,
        llm_enhance: None,
        auto_create: 0,
        min_score: 0.0,
    }
}

/// Helper: Create a search query with auto_create=1
fn search_query_with_create(dialogue: &str) -> SearchQuery {
    SearchQuery {
        dialogue: dialogue.to_string(),
        context_id: None,
        l3_id: None,
        context_limit: 10,
        llm_enhance: None,
        auto_create: 1,
        min_score: 0.0,
    }
}

/// Helper: Create a search query with context_id filter
fn search_query_with_context_id(dialogue: &str, context_id: &str) -> SearchQuery {
    SearchQuery {
        dialogue: dialogue.to_string(),
        context_id: Some(context_id.to_string()),
        l3_id: None,
        context_limit: 10,
        llm_enhance: None,
        auto_create: 0,
        min_score: 0.0,
    }
}

// ============================================================================
// Test Scenario 1: Empty database + auto_create=0
// ============================================================================

#[test]
fn test_search_empty_db_auto_create_0() {
    let (_temp_dir, mut db) = create_test_db("search_empty_no_create");

    // Search with auto_create=0 on empty database
    let query = search_query_no_create("test dialogue about Rust programming");
    let result = db.search_memory(query).unwrap();

    // Should return empty results
    assert!(result.contexts.is_empty(), "L2 contexts should be empty on empty database");
    assert!(result.l3_ids.is_empty(), "L3 IDs should be empty");
    assert!(result.archive_refs.is_empty(), "Archive refs should be empty");
    assert!(result.profile.is_none(), "L0 profile should be None on empty database");
}

// ============================================================================
// Test Scenario 2: Empty database + auto_create=1 (auto-create L2)
// ============================================================================

#[test]
fn test_search_empty_db_auto_create_1() {
    let (_temp_dir, mut db) = create_test_db("search_empty_auto_create");

    // Search with auto_create=1 on empty database
    let query = search_query_with_create("Learn Rust ownership and borrowing rules");
    let result = db.search_memory(query).unwrap();

    // Should automatically create a new L2 context
    assert_eq!(result.contexts.len(), 1, "Should have exactly 1 auto-created L2 context");

    let ctx = &result.contexts[0];
    assert!(!ctx.id.is_empty(), "Context ID should not be empty");
    assert!(ctx.title.contains("Learn Rust"), "Context title should contain first part of dialogue");

    // Verify archive_refs is empty for auto-created
    assert!(ctx.archive_refs.is_empty(), "Auto-created context should have no archive refs");
}

// ============================================================================
// Test Scenario 3: Existing data + auto_create=0 normal retrieval
// ============================================================================

#[test]
fn test_search_with_data_auto_create_0() {
    let (_temp_dir, mut db) = create_test_db("search_with_data");

    // First, create a context via auto_create=1
    let query1 = search_query_with_create("Rust programming language fundamentals");
    let result1 = db.search_memory(query1).unwrap();
    assert_eq!(result1.contexts.len(), 1, "Should create 1 context");

    let created_context_id = result1.contexts[0].id.clone();

    // Now search with auto_create=0 using similar keywords
    let query2 = search_query_no_create("Rust programming");
    let result2 = db.search_memory(query2).unwrap();

    // Should find the existing context via BM25
    assert!(!result2.contexts.is_empty(), "Should find existing context");
    assert!(
        result2.contexts.iter().any(|c| c.id == created_context_id),
        "Should find the previously created context"
    );
}

// ============================================================================
// Test Scenario 4: Existing data + auto_create=1 when search returns empty
// ============================================================================

#[test]
fn test_search_with_data_auto_create_1_empty_result() {
    let (_temp_dir, mut db) = create_test_db("search_auto_create_empty");

    // First, create a context
    let query1 = search_query_with_create("Python data science");
    let result1 = db.search_memory(query1).unwrap();
    assert_eq!(result1.contexts.len(), 1);

    // Search for something completely different
    let query2 = search_query_with_create("Quantum physics theory");
    let result2 = db.search_memory(query2).unwrap();

    // Should create a new context because the query is unrelated
    assert_eq!(result2.contexts.len(), 1, "Should create new context for unrelated query");

    // Verify it's a different context
    assert_ne!(
        result2.contexts[0].id, result1.contexts[0].id,
        "Should create a different context"
    );
}

// ============================================================================
// Test Scenario 5: context_id exact filter
// ============================================================================

#[test]
fn test_search_with_context_id_filter() {
    let (_temp_dir, mut db) = create_test_db("search_context_id_filter");

    // Create two contexts
    let query1 = search_query_with_create("Context A: Machine learning basics");
    let result1 = db.search_memory(query1).unwrap();
    let context_a_id = result1.contexts[0].id.clone();

    let query2 = search_query_with_create("Context B: Web development");
    let result2 = db.search_memory(query2).unwrap();
    let _context_b_id = result2.contexts[0].id.clone();

    // Search with context_id filter for Context A
    let query3 = search_query_with_context_id("Context A Machine learning basics", &context_a_id);
    let result3 = db.search_memory(query3).unwrap();

    // Should find Context A via context_id
    assert!(!result3.contexts.is_empty(), "Should find context via context_id");

    // Search with non-existent context_id
    let query5 = search_query_with_context_id("anything", "nonexistentid123456");
    let result5 = db.search_memory(query5).unwrap();
    // Result depends on implementation - just verify no panic
    println!("Non-existent context_id result: {} contexts", result5.contexts.len());
}

// ============================================================================
// Test Scenario 6: Verify created L2 can be retrieved again
// ============================================================================

#[test]
fn test_search_auto_create_then_retrieve() {
    let (_temp_dir, mut db) = create_test_db("search_create_retrieve");

    // Create a context via auto_create
    let dialogue = "Learn about neural networks and deep learning architectures";
    let query1 = search_query_with_create(dialogue);
    let result1 = db.search_memory(query1).unwrap();
    assert_eq!(result1.contexts.len(), 1);

    let created_id = result1.contexts[0].id.clone();
    let created_title = result1.contexts[0].title.clone();

    // Search again with same keywords (auto_create=0)
    let query2 = search_query_no_create("neural networks deep learning");
    let result2 = db.search_memory(query2).unwrap();

    // Should find the same context
    assert!(!result2.contexts.is_empty(), "Should find the context");
    let found = result2.contexts.iter().find(|c| c.id == created_id);
    assert!(found.is_some(), "Should find the exact same context by ID");
    assert_eq!(found.unwrap().title, created_title, "Title should match");
}

// ============================================================================
// Test Scenario 7: Multiple auto_creates produce different contexts
// ============================================================================

#[test]
fn test_search_multiple_auto_creates() {
    let (_temp_dir, mut db) = create_test_db("search_multiple_creates");

    // Create multiple contexts with different dialogues
    let dialogues = vec![
        "Machine learning neural networks deep learning 1",
        "Web development React TypeScript frontend 2",
        "Quantum physics theory relativity particles 3",
    ];

    let mut created_ids = Vec::new();

    for dialogue in &dialogues {
        let query = search_query_with_create(dialogue);
        let result = db.search_memory(query).unwrap();
        assert_eq!(result.contexts.len(), 1, "Should create 1 context per dialogue");
        created_ids.push(result.contexts[0].id.clone());
    }

    // Verify all IDs are unique
    let unique_ids: std::collections::HashSet<_> = created_ids.iter().collect();
    assert_eq!(unique_ids.len(), dialogues.len(), "All context IDs should be unique");
}

// ============================================================================
// Test Scenario 8: auto_create with context_limit
// ============================================================================

#[test]
fn test_search_auto_create_with_context_limit() {
    let (_temp_dir, mut db) = create_test_db("search_limit");

    // Create multiple contexts
    for i in 0..5 {
        let dialogue = format!("Context number {} about various subjects", i);
        let query = search_query_with_create(&dialogue);
        let result = db.search_memory(query).unwrap();
        assert_eq!(result.contexts.len(), 1);
    }

    // Search with context_limit=2
    let mut query = search_query_no_create("context");
    query.context_limit = 2;
    let result = db.search_memory(query).unwrap();

    // Should respect the limit
    assert!(result.contexts.len() <= 2, "Should respect context_limit");
}

// ============================================================================
// Test Scenario 9: auto_create=1 always creates new L2 (no duplicate check)
// ============================================================================

#[test]
fn test_search_auto_create_always_creates() {
    let (_temp_dir, mut db) = create_test_db("search_always_creates");

    // Create a context
    let dialogue = "Rust ownership and borrowing rules explained";
    let query1 = search_query_with_create(dialogue);
    let result1 = db.search_memory(query1).unwrap();
    assert_eq!(result1.contexts.len(), 1);
    let first_id = result1.contexts[0].id.clone();

    // Search again with same dialogue and auto_create=1
    let query2 = search_query_with_create(dialogue);
    let result2 = db.search_memory(query2).unwrap();

    // auto_create=1 always creates a new L2 context (skip retrieval)
    assert_eq!(result2.contexts.len(), 1, "Should create a new context");
    assert_ne!(result2.contexts[0].id, first_id, "New context ID should be different");
}

// ============================================================================
// Test Scenario 10: Edge case - empty dialogue
// ============================================================================

#[test]
fn test_search_empty_dialogue() {
    let (_temp_dir, mut db) = create_test_db("search_empty_dialogue");

    // Search with empty dialogue
    let query = search_query_with_create("");
    let result = db.search_memory(query).unwrap();

    // Should still create a context (even with empty title)
    // or return empty - depends on implementation
    // For now, just verify it doesn't panic
    println!("Empty dialogue result: {} L2 contexts", result.contexts.len());
}

// ============================================================================
// Test Scenario 11: Edge case - very long dialogue
// ============================================================================

#[test]
fn test_search_long_dialogue() {
    let (_temp_dir, mut db) = create_test_db("search_long_dialogue");

    // Create a very long dialogue (should be truncated to 50 chars for title)
    let long_dialogue = "A".repeat(1000);
    let query = search_query_with_create(&long_dialogue);
    let result = db.search_memory(query).unwrap();

    assert_eq!(result.contexts.len(), 1, "Should create context from long dialogue");

    // Title should be truncated to 50 characters
    let ctx = &result.contexts[0];
    assert!(ctx.title.len() <= 50, "Title should be truncated to 50 chars");
}

// ============================================================================
// Test Scenario 12: Edge case - special characters in dialogue
// ============================================================================

#[test]
fn test_search_special_characters() {
    let (_temp_dir, mut db) = create_test_db("search_special_chars");

    // Search with special characters
    let dialogue = "Hello! @world #test $100 %off ^power &more *star";
    let query = search_query_with_create(dialogue);
    let result = db.search_memory(query).unwrap();

    assert_eq!(result.contexts.len(), 1, "Should handle special characters");
    assert!(!result.contexts[0].id.is_empty(), "Should have valid ID");
}

// ============================================================================
// Test Scenario 13: Auto-create with Chinese characters
// ============================================================================

#[test]
fn test_search_auto_create_chinese() {
    let (_temp_dir, mut db) = create_test_db("search_chinese");

    // Search with Chinese dialogue
    let dialogue = "学习Rust编程语言的所有权和借用规则";
    let query = search_query_with_create(dialogue);
    let result = db.search_memory(query).unwrap();

    assert_eq!(result.contexts.len(), 1, "Should handle Chinese characters");
    let ctx = &result.contexts[0];
    assert!(ctx.title.contains("学习Rust"), "Title should contain Chinese text");
}

// ============================================================================
// Test Scenario 14: Verify L0 profile is None when not set
// ============================================================================

#[test]
fn test_search_l0_profile_none() {
    let (_temp_dir, mut db) = create_test_db("search_l0_none");

    // Search on fresh database
    let query = search_query_with_create("test");
    let result = db.search_memory(query).unwrap();

    // L0 profile should be None since we haven't set it
    assert!(result.profile.is_none(), "L0 profile should be None on fresh database");
}

// ============================================================================
// Test Scenario 15: Verify l3_ids is empty on auto-created contexts
// ============================================================================

#[test]
fn test_search_auto_create_no_l3() {
    let (_temp_dir, mut db) = create_test_db("search_no_l3");

    // Create a context via auto_create
    let query = search_query_with_create("New context without knowledge");
    let result = db.search_memory(query).unwrap();

    assert_eq!(result.contexts.len(), 1);
    assert!(result.l3_ids.is_empty(), "Auto-created context should have no L3 refs");
}
