// Compile-time + runtime smoke test for the 12 public APIs.
//
// This test does not assert deep semantics; it only verifies that every API
// listed in API.md is reachable from an external crate and accepts the
// documented argument types.

use memhop::{
    L4SearchQuery, L6Filter, MemHop, MemHopConfig, PathwayWeightSlot, ProfileResult, SearchQuery,
    SearchResult, TopicListQuery, UpdateL2Fields, UpdateL3Fields, UpdateL5Fields, UpdateL6Fields,
    UpdateProfileRequest, UpdateRequest, UpdateResult,
};

fn make_config(path: std::path::PathBuf) -> MemHopConfig {
    let mut config = MemHopConfig::new(path, 768);
    config.encoder_grpc_addr = None;
    config
}

fn create_topic(db: &mut MemHop, dialogue: &str) -> String {
    db.search_context(SearchQuery {
        dialogue: dialogue.into(),
        l2_id: None,
        context_id: None,
        l3_id: None,
        context_limit: 5,
        auto_create: 1,
        min_score: 0.0,
        source: Default::default(),
    })
    .unwrap()
    .contexts
    .into_iter()
    .next()
    .map(|c| c.id)
    .expect("auto_create should yield a context")
}

#[test]
fn api_surface_is_reachable() {
    let dir = tempfile::TempDir::new().unwrap();
    let path = dir.path().join("surface.meh");

    // API-1 open
    let mut db = MemHop::open(make_config(path)).unwrap();

    // API-2 search_context
    let topic_a = create_topic(&mut db, "first topic");
    let topic_b = create_topic(&mut db, "second topic");

    let _: SearchResult = db
        .search_context(SearchQuery {
            dialogue: "hello".into(),
            l2_id: None,
            context_id: None,
            l3_id: None,
            context_limit: 5,
            auto_create: 0,
            min_score: 0.0,
            source: Default::default(),
        })
        .unwrap();

    // API-3 update_memory
    let _: UpdateResult = db
        .update_memory(UpdateRequest {
            topic_id: topic_a.clone(),
            dialogue_text: "turn".into(),
            summary: None,
            action_chain: None,
            instant_distill: false,
            source: Default::default(),
        })
        .unwrap();

    // API-4 profile
    let _: Option<ProfileResult> = db.get_profile().unwrap();
    let _ = db
        .update_profile(UpdateProfileRequest {
            name: Some("Agent".into()),
            role: None,
            personality: None,
            worldview: None,
            preferences: None,
            lexicon: None,
            style_traits: None,
            emotion_patterns: None,
        })
        .unwrap();

    // API-5 L2 CRUD
    let _ = db.list_l2(TopicListQuery {
        page: 1,
        page_size: 10,
        active_only: false,
        keyword: None,
    });
    let _ = db.get_l2(&topic_a);
    let _ = db.update_l2(
        &topic_a,
        UpdateL2Fields {
            title: Some("t".into()),
            ..Default::default()
        },
    );
    let _ = db.delete_turn(&topic_a, 0..0);

    // API-6 merge_l2
    let _ = db.merge_l2(&topic_a, vec![topic_b.clone()]);

    // API-7 L3 CRUD
    let _ = db.get_l3("0000000000000003");
    let _ = db.update_l3(
        "0000000000000003",
        UpdateL3Fields {
            name: Some("k".into()),
        },
    );
    let _ = db.delete_l3("0000000000000003");

    // API-8 L4 search
    let _ = db.search_l4(L4SearchQuery {
        recent: Some(5),
        ..Default::default()
    });

    // API-9 L5 CRUD
    let _ = db.get_l5("0000000000000006");
    let _ = db.update_l5(
        "0000000000000006",
        UpdateL5Fields {
            title: Some("c".into()),
            ..Default::default()
        },
    );
    let _ = db.delete_l5("0000000000000006");

    // API-10 L6 CRUD
    let _ = db.get_l6("0000000000000007");
    let _ = db.update_l6(
        "0000000000000007",
        UpdateL6Fields {
            weight: Some(0.5),
            ..Default::default()
        },
    );
    let _ = db.delete_l6("0000000000000007");
    let _ = db.list_l6(Some(L6Filter {
        min_weight: Some(0.1),
        ..Default::default()
    }));
    let _ = db.add_l6(PathwayWeightSlot {
        id_hash: 1,
        source_node: "s".into(),
        target_node: "t".into(),
        weight: 0.5,
        trigger_count: 0,
        success_rate: 0.0,
        last_accessed: 0,
        metadata: String::new(),
        created_at: 0,
        updated_at: 0,
        version: 1,
    });
    let _ = db.update_l6_weight("0000000000000001", 0.1);

    // API-11 dream
    let _ = db.dream(None);

    // API-12 close
    db.close().unwrap();
}
