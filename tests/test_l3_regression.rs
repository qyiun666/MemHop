//! Regression tests for L3 Knowledge API (Interfaces 9, 10, 15)
//!
//! Tests get_knowledge(), list_knowledge(), update_knowledge_title()
//! using the l3 engine integration.

use memhop::query::types::*;
use memhop::{MemHop, MemHopConfig};
use tempfile::TempDir;

mod common;
use common::test_encoder::TestEncoder;

/// Helper: Create a new MemHop database instance
fn create_test_db(name: &str) -> (TempDir, MemHop) {
    let temp_dir = TempDir::new().unwrap();
    let db_path = temp_dir.path().join(format!("{}.meh", name));
    let config = MemHopConfig {
        db_path: db_path.clone(),
        encoder_grpc_addr: None,
        vector_dim: 384,
        crystal_path: None,
    };
    let mut db = MemHop::open(config).unwrap();
    db.set_encoder(TestEncoder::new(384));
    (temp_dir, db)
}

/// Helper: Import a single knowledge item and return the graph_id (hex)
fn import_knowledge(
    db: &mut MemHop,
    title: &str,
    domain: &str,
    knowledge_type: &str,
    text: &str,
) -> String {
    let request = ImportRequest {
        target_layer: TargetLayer::Knowledge,
        data: ImportData::Knowledge(vec![KnowledgeImportItem {
            title: title.to_string(),
            domain: domain.to_string(),
            knowledge_type: knowledge_type.to_string(),
            text: text.to_string(),
            summary: Some(format!("Summary of {}", title)),
            keywords: vec![title.to_lowercase()],
            source_ref: None,
        }]),
        mode: ImportMode::Merge,
        knowledge_title: None,
    };
    let result = db.import_memory(request).unwrap();
    assert!(!result.created_ids.is_empty(), "Should create node(s)");
    result.created_ids[0].clone()
}

/// Helper: Compute HypergraphSlot ID for a domain
fn domain_graph_id(domain: &str) -> String {
    use memhop::query::common;
    let hash = common::parse_id_to_hash(domain);
    common::format_hash(hash)
}

// ============================================================================
// Test 1: Get knowledge by ID
// ============================================================================

#[test]
fn test_get_knowledge_by_id() {
    let (_temp_dir, mut db) = create_test_db("l3_get_by_id");

    // Import knowledge
    let _node_id = import_knowledge(
        &mut db,
        "Rust Ownership",
        "programming",
        "Conceptual",
        "Rust ownership system ensures memory safety through borrow checking",
    );

    // The graph_id = hash of domain "programming"
    let graph_id = domain_graph_id("programming");

    // Query by graph_id (HypergraphSlot domain name = "manual" for Manual source)
    let result = db.get_knowledge(&graph_id).unwrap();
    assert!(result.is_some(), "Should find knowledge by graph_id");

    let detail = result.unwrap();
    assert!(
        detail.title.contains("programming"),
        "Title should contain domain name"
    );
    assert!(
        detail.text.contains("Rust ownership"),
        "Text should contain imported content"
    );
    assert!(!detail.keywords.is_empty(), "Keywords should not be empty");
    assert!(detail.created_at > 0, "Created timestamp should be set");
    assert!(
        detail.updated_at >= detail.created_at,
        "Updated should >= created"
    );

    println!(
        "✅ test_get_knowledge_by_id: title='{}' text={}chars",
        detail.title,
        detail.text.len()
    );
}

// ============================================================================
// Test 2: Get knowledge with non-existent ID
// ============================================================================

#[test]
fn test_get_knowledge_not_found() {
    let (_temp_dir, mut db) = create_test_db("l3_get_not_found");

    // Import some data first (ensure DB is initialized)
    import_knowledge(&mut db, "Dummy", "test", "Conceptual", "dummy");

    // Query with non-existent hex ID
    let result = db.get_knowledge("ffffffffffffffff").unwrap();
    assert!(result.is_none(), "Non-existent ID should return None");

    println!("✅ test_get_knowledge_not_found passed");
}

// ============================================================================
// Test 3: Update knowledge title
// ============================================================================

#[test]
fn test_update_knowledge_title() {
    let (_temp_dir, mut db) = create_test_db("l3_update_title");

    // Import knowledge
    import_knowledge(
        &mut db,
        "Rust Ownership",
        "programming",
        "Conceptual",
        "Rust ownership system ensures memory safety",
    );

    let graph_id = domain_graph_id("programming");

    // Verify initial title
    let before = db.get_knowledge(&graph_id).unwrap().unwrap();
    let initial_title = before.title.clone();

    // Update title
    let new_title = "Advanced Rust Programming Concepts".to_string();
    let summary = db
        .update_knowledge_title(&graph_id, new_title.clone())
        .unwrap();
    assert_eq!(summary.title, new_title, "Title should be updated");
    assert!(
        summary.updated_at >= before.updated_at,
        "Updated_at should advance"
    );

    // Verify persistence by reading again
    let after = db.get_knowledge(&graph_id).unwrap().unwrap();
    assert_eq!(after.title, new_title, "Title change should persist");
    assert_ne!(
        after.title, initial_title,
        "New title should differ from old"
    );

    println!(
        "✅ test_update_knowledge_title: '{}' -> '{}'",
        initial_title, after.title
    );
}

// ============================================================================
// Test 4: List knowledge pagination
// ============================================================================

#[test]
fn test_list_knowledge_pagination() {
    let (_temp_dir, mut db) = create_test_db("l3_list_pagination");

    // Import 5 knowledge items with different domains
    let domains = ["programming", "math", "physics", "chemistry", "biology"];
    for (i, domain) in domains.iter().enumerate() {
        import_knowledge(
            &mut db,
            &format!("Topic {}", i),
            domain,
            "Conceptual",
            &format!("Content about {}", domain),
        );
    }

    // Query page 1 with page_size=3
    let q1 = KnowledgeListQuery {
        page: 1,
        page_size: 3,
        domain_filter: None,
        knowledge_type: None,
        keyword: None,
    };
    let r1 = db.list_knowledge(q1).unwrap();
    assert_eq!(r1.total, 5, "Total should be 5");
    assert_eq!(r1.items.len(), 3, "Page 1 should have 3 items");
    assert!(r1.has_more, "Page 1 should have more");
    assert_eq!(r1.page, 1);
    assert_eq!(r1.page_size, 3);

    // Query page 2 with page_size=3
    let q2 = KnowledgeListQuery {
        page: 2,
        page_size: 3,
        domain_filter: None,
        knowledge_type: None,
        keyword: None,
    };
    let r2 = db.list_knowledge(q2).unwrap();
    assert_eq!(r2.total, 5, "Total should still be 5");
    assert_eq!(r2.items.len(), 2, "Page 2 should have 2 items");
    assert!(!r2.has_more, "Page 2 should have no more");

    // Verify items have domain names (not Rust Debug format)
    for item in &r2.items {
        assert!(
            item.domain.len() >= 4,
            "Domain should be meaningful string, got '{}'",
            item.domain
        );
    }

    // Query all (page_size=10)
    let q3 = KnowledgeListQuery {
        page: 1,
        page_size: 10,
        domain_filter: None,
        knowledge_type: None,
        keyword: None,
    };
    let r3 = db.list_knowledge(q3).unwrap();
    assert_eq!(r3.items.len(), 5, "Should return all 5 items");

    println!(
        "✅ test_list_knowledge_pagination: total={}, page1={}, page2={}",
        r1.total,
        r1.items.len(),
        r2.items.len()
    );
}
