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
        l2_id: None,
        l3_id: None,
        l2_limit: 10,
        l3_limit: 10,
        llm_enhance: None,
        auto_create: 0,
    }
}

/// Helper: Create a search query with auto_create=1
fn search_query_with_create(dialogue: &str) -> SearchQuery {
    SearchQuery {
        dialogue: dialogue.to_string(),
        l2_id: None,
        l3_id: None,
        l2_limit: 10,
        l3_limit: 10,
        llm_enhance: None,
        auto_create: 1,
    }
}

/// Helper: Create a search query with l2_id filter
fn search_query_with_l2_id(dialogue: &str, l2_id: &str) -> SearchQuery {
    SearchQuery {
        dialogue: dialogue.to_string(),
        l2_id: Some(l2_id.to_string()),
        l3_id: None,
        l2_limit: 10,
        l3_limit: 10,
        llm_enhance: None,
        auto_create: 0,
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
    assert!(result.l2_topics.is_empty(), "L2 topics should be empty on empty database");
    assert!(result.l3_knowledge.is_empty(), "L3 knowledge should be empty");
    assert!(result.l4_archives.is_empty(), "L4 archives should be empty");
    assert!(result.memory_ids.is_empty(), "Memory IDs should be empty");
    assert!(result.l0_profile.is_none(), "L0 profile should be None on empty database");
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

    // Should automatically create a new L2 topic
    assert_eq!(result.l2_topics.len(), 1, "Should have exactly 1 auto-created L2 topic");

    let topic = &result.l2_topics[0];
    assert!(!topic.id.is_empty(), "Topic ID should not be empty");
    assert!(topic.title.contains("Learn Rust"), "Topic title should contain first 50 chars of dialogue");
    assert!(topic.activation_score > 0.0, "Activation score should be positive");

    // Memory IDs should contain the new topic
    assert_eq!(result.memory_ids.len(), 1, "Should have 1 memory ID");
    assert_eq!(result.memory_ids[0], topic.id, "Memory ID should match topic ID");
}

// ============================================================================
// Test Scenario 3: Existing data + auto_create=0 normal retrieval
// ============================================================================

#[test]
fn test_search_with_data_auto_create_0() {
    let (_temp_dir, mut db) = create_test_db("search_with_data");

    // First, create a topic via auto_create=1
    let query1 = search_query_with_create("Rust programming language fundamentals");
    let result1 = db.search_memory(query1).unwrap();
    assert_eq!(result1.l2_topics.len(), 1, "Should create 1 topic");

    let created_topic_id = result1.l2_topics[0].id.clone();

    // Now search with auto_create=0 using similar keywords
    let query2 = search_query_no_create("Rust programming");
    let result2 = db.search_memory(query2).unwrap();

    // Should find the existing topic via BM25
    assert!(!result2.l2_topics.is_empty(), "Should find existing topic");
    assert!(
        result2.l2_topics.iter().any(|t| t.id == created_topic_id),
        "Should find the previously created topic"
    );
}

// ============================================================================
// Test Scenario 4: Existing data + auto_create=1 when search returns empty
// ============================================================================

#[test]
fn test_search_with_data_auto_create_1_empty_result() {
    let (_temp_dir, mut db) = create_test_db("search_auto_create_empty");

    // First, create a topic
    let query1 = search_query_with_create("Python data science");
    let result1 = db.search_memory(query1).unwrap();
    assert_eq!(result1.l2_topics.len(), 1);

    // Search for something completely different
    let query2 = search_query_with_create("Quantum physics theory");
    let result2 = db.search_memory(query2).unwrap();

    // Should create a new topic because the query is unrelated
    assert_eq!(result2.l2_topics.len(), 1, "Should create new topic for unrelated query");

    // Verify it's a different topic
    assert_ne!(
        result2.l2_topics[0].id, result1.l2_topics[0].id,
        "Should create a different topic"
    );
}

// ============================================================================
// Test Scenario 5: l2_id exact filter
// ============================================================================

#[test]
fn test_search_with_l2_id_filter() {
    let (_temp_dir, mut db) = create_test_db("search_l2_id_filter");

    // Create two topics
    let query1 = search_query_with_create("Topic A: Machine learning basics");
    let result1 = db.search_memory(query1).unwrap();
    let topic_a_id = result1.l2_topics[0].id.clone();

    let query2 = search_query_with_create("Topic B: Web development");
    let result2 = db.search_memory(query2).unwrap();
    let topic_b_id = result2.l2_topics[0].id.clone();

    // Search with l2_id filter for Topic A
    let query3 = search_query_with_l2_id("Topic A Machine learning basics", &topic_a_id);
    let result3 = db.search_memory(query3).unwrap();

    // Should only return Topic A
    assert_eq!(result3.l2_topics.len(), 1, "Should find exactly 1 topic");
    assert_eq!(result3.l2_topics[0].id, topic_a_id, "Should find Topic A");

    // Search with l2_id filter for Topic B
    let query4 = search_query_with_l2_id("Topic B Web development", &topic_b_id);
    let result4 = db.search_memory(query4).unwrap();

    // Should only return Topic B
    assert_eq!(result4.l2_topics.len(), 1, "Should find exactly 1 topic");
    assert_eq!(result4.l2_topics[0].id, topic_b_id, "Should find Topic B");

    // Search with non-existent l2_id
    let query5 = search_query_with_l2_id("anything", "nonexistentid123456");
    let result5 = db.search_memory(query5).unwrap();
    assert!(result5.l2_topics.is_empty(), "Should return empty for non-existent l2_id");
}

// ============================================================================
// Test Scenario 6: Verify created L2 can be retrieved again
// ============================================================================

#[test]
fn test_search_auto_create_then_retrieve() {
    let (_temp_dir, mut db) = create_test_db("search_create_retrieve");

    // Create a topic via auto_create
    let dialogue = "Learn about neural networks and deep learning architectures";
    let query1 = search_query_with_create(dialogue);
    let result1 = db.search_memory(query1).unwrap();
    assert_eq!(result1.l2_topics.len(), 1);

    let created_id = result1.l2_topics[0].id.clone();
    let created_title = result1.l2_topics[0].title.clone();

    // Search again with same keywords (auto_create=0)
    let query2 = search_query_no_create("neural networks deep learning");
    let result2 = db.search_memory(query2).unwrap();

    // Should find the same topic
    assert!(!result2.l2_topics.is_empty(), "Should find the topic");
    let found = result2.l2_topics.iter().find(|t| t.id == created_id);
    assert!(found.is_some(), "Should find the exact same topic by ID");
    assert_eq!(found.unwrap().title, created_title, "Title should match");
}

// ============================================================================
// Test Scenario 7: Multiple auto_creates produce different topics
// ============================================================================

#[test]
fn test_search_multiple_auto_creates() {
    let (_temp_dir, mut db) = create_test_db("search_multiple_creates");

    // Create multiple topics with different dialogues
    let dialogues = vec![
        "Machine learning neural networks deep learning 1",
        "Web development React TypeScript frontend 2",
        "Quantum physics theory relativity particles 3",
    ];

    let mut created_ids = Vec::new();

    for dialogue in &dialogues {
        let query = search_query_with_create(dialogue);
        let result = db.search_memory(query).unwrap();
        assert_eq!(result.l2_topics.len(), 1, "Should create 1 topic per dialogue");
        created_ids.push(result.l2_topics[0].id.clone());
    }

    // Verify all IDs are unique
    let unique_ids: std::collections::HashSet<_> = created_ids.iter().collect();
    assert_eq!(unique_ids.len(), dialogues.len(), "All topic IDs should be unique");
}

// ============================================================================
// Test Scenario 8: auto_create with l2_limit
// ============================================================================

#[test]
fn test_search_auto_create_with_l2_limit() {
    let (_temp_dir, mut db) = create_test_db("search_limit");

    // Create multiple topics
    for i in 0..5 {
        let dialogue = format!("Topic number {} about various subjects", i);
        let query = search_query_with_create(&dialogue);
        let result = db.search_memory(query).unwrap();
        assert_eq!(result.l2_topics.len(), 1);
    }

    // Search with l2_limit=2
    let mut query = search_query_no_create("topic");
    query.l2_limit = 2;
    let result = db.search_memory(query).unwrap();

    // Should respect the limit
    assert!(result.l2_topics.len() <= 2, "Should respect l2_limit");
}

// ============================================================================
// Test Scenario 9: auto_create=1 always creates new L2 (no duplicate check)
// ============================================================================

#[test]
fn test_search_auto_create_always_creates() {
    let (_temp_dir, mut db) = create_test_db("search_always_creates");

    // Create a topic
    let dialogue = "Rust ownership and borrowing rules explained";
    let query1 = search_query_with_create(dialogue);
    let result1 = db.search_memory(query1).unwrap();
    assert_eq!(result1.l2_topics.len(), 1);
    let first_id = result1.l2_topics[0].id.clone();

    // Search again with same dialogue and auto_create=1
    let query2 = search_query_with_create(dialogue);
    let result2 = db.search_memory(query2).unwrap();

    // auto_create=1 always creates a new L2 topic (skip retrieval)
    assert_eq!(result2.l2_topics.len(), 1, "Should create a new topic");
    assert_ne!(result2.l2_topics[0].id, first_id, "New topic ID should be different");
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

    // Should still create a topic (even with empty title)
    // or return empty - depends on implementation
    // For now, just verify it doesn't panic
    println!("Empty dialogue result: {} L2 topics", result.l2_topics.len());
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

    assert_eq!(result.l2_topics.len(), 1, "Should create topic from long dialogue");

    // Title should be truncated to 50 characters
    let topic = &result.l2_topics[0];
    assert!(topic.title.len() <= 50, "Title should be truncated to 50 chars");
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

    assert_eq!(result.l2_topics.len(), 1, "Should handle special characters");
    assert!(!result.l2_topics[0].id.is_empty(), "Should have valid ID");
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

    assert_eq!(result.l2_topics.len(), 1, "Should handle Chinese characters");
    let topic = &result.l2_topics[0];
    assert!(topic.title.contains("学习Rust"), "Title should contain Chinese text");
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
    assert!(result.l0_profile.is_none(), "L0 profile should be None on fresh database");
}

// ============================================================================
// Test Scenario 15: Verify l3_knowledge is empty on auto-created topics
// ============================================================================

#[test]
fn test_search_auto_create_no_l3() {
    let (_temp_dir, mut db) = create_test_db("search_no_l3");

    // Create a topic via auto_create
    let query = search_query_with_create("New topic without knowledge");
    let result = db.search_memory(query).unwrap();

    assert_eq!(result.l2_topics.len(), 1);
    assert!(result.l3_knowledge.is_empty(), "Auto-created topic should have no L3 knowledge");
    assert!(result.l4_archives.is_empty(), "Auto-created topic should have no L4 archives");
}
