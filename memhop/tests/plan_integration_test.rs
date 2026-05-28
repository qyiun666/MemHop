use std::collections::HashMap;

use memhop::{
    EmotionalState, PlanHint, Protection, ToneMeta, StyleCompact,
    plan_gate::{PlanGate, PlanIndex, PlanContext},
    PerceptionInput, BrainConfig,
    VECTOR_DIM,
};

fn make_gate() -> PlanGate {
    PlanGate::new(0.55, 3, 24)
}

fn perception(text: &str, session: &str) -> PerceptionInput {
    PerceptionInput {
        content: text.to_string(),
        vector: vec![half::f16::from_f32(0.0); VECTOR_DIM],
        emotional_state: EmotionalState::new(0.0, 0.5),
        attention_anchors: vec![],
        perceived_importance: 0.5,
        session_id: session.to_string(),
        protection: Protection::Normal,
        manual_links: vec![],
        meta: HashMap::new(),
        plan_id: None,
        agent_response: None,
        dialogue_timestamp: None,
        source: None,
    }
}

fn make_tone(valence: f32, arousal: f32) -> ToneMeta {
    ToneMeta {
        valence,
        arousal,
        tone_tags: Vec::new(),
        filler_ratio: 0.0,
        sentence_style: StyleCompact {
            avg_sentence_len: 0.0,
            question_ratio: 0.0,
            exclamation_count: 0,
        },
    }
}

#[test]
fn test_boundary_score_same() {
    let gate = make_gate();
    let v = vec![1.0_f32; 1024];
    let tone = make_tone(0.5, 0.5);
    let score = gate.boundary_score(
        &v,
        &tone,
        &[],
        PlanContext { centroid: Some(&v), avg_tone: Some(&tone), anchors: &[] },
        0.0,
    );
    assert!(score < 0.01, "same vector should give ~0, got {}", score);
}

#[test]
fn test_boundary_score_high() {
    let gate = make_gate();
    let cur = vec![1.0; 1024];
    let cent: Vec<f32> = vec![-1.0; 1024];
    let tone = make_tone(0.0, 0.5);
    let score = gate.boundary_score(
        &cur,
        &tone,
        &[],
        PlanContext { centroid: Some(&cent), avg_tone: Some(&tone), anchors: &[] },
        0.0,
    );
    assert!(score > 0.35, "opposite vectors should > 0.35, got {}", score);
}

#[test]
fn test_boundary_score_no_centroid() {
    let gate = make_gate();
    let v: Vec<f32> = vec![0.0; 1024];
    let tone = make_tone(0.0, 0.5);
    let score = gate.boundary_score(
        &v,
        &tone,
        &[],
        PlanContext { centroid: None, avg_tone: None, anchors: &[] },
        0.0,
    );
    assert!(score < 0.01);
}

#[test]
fn test_boundary_score_emotional_shift() {
    let gate = make_gate();
    let v: Vec<f32> = vec![0.0; 1024];
    let cur = make_tone(1.0, 0.0);
    let avg = make_tone(-1.0, 1.0);
    let score = gate.boundary_score(
        &v,
        &cur,
        &[],
        PlanContext { centroid: None, avg_tone: Some(&avg), anchors: &[] },
        0.0,
    );
    assert!(score >= 0.35 && score <= 0.40, "emotional shift, got {}", score);
}

#[test]
fn test_boundary_score_anchor_change() {
    let gate = make_gate();
    let v: Vec<f32> = vec![0.0; 1024];
    let tone = make_tone(0.0, 0.5);
    let cur = vec!["auth".to_string(), "jwt".to_string()];
    let prev = vec!["storage".to_string(), "upload".to_string()];
    let score = gate.boundary_score(
        &v,
        &tone,
        &cur,
        PlanContext { centroid: None, avg_tone: None, anchors: &prev },
        0.0,
    );
    assert!(score >= 0.20 && score <= 0.30, "disjoint anchors, got {}", score);
}

#[test]
fn test_decide_basic() {
    let mut gate = PlanGate::new(0.55, 3, 24);
    assert_eq!(gate.decide(0.9, 1000), PlanHint::Continuing);
}

#[test]
fn test_decide_new_topic() {
    let mut gate = PlanGate::new(0.55, 3, 24);
    gate.decide(0.9, 1000);
    gate.decide(0.8, 2000);
    assert_eq!(gate.decide(0.7, 3000), PlanHint::NewTopicLikely);
}

#[test]
fn test_decide_timeout() {
    let mut gate = PlanGate::new(0.55, 3, 24);
    gate.decide(0.1, 1000);
    gate.decide(0.1, 2000);
    let gap = 25 * 3600 * 1000;
    assert_eq!(gate.decide(0.1, 2000 + gap), PlanHint::TimeoutNewPlan);
}

#[test]
fn test_match_to_plan_explicit() {
    let gate = make_gate();
    let index = PlanIndex::new();
    assert_eq!(
        gate.match_to_plan(Some("plan_1"), &index, &[], 0.0),
        Some("plan_1".to_string())
    );
}

#[test]
fn test_match_to_plan_no_active() {
    let gate = make_gate();
    let index = PlanIndex::new();
    assert_eq!(gate.match_to_plan(None, &index, &[], 0.0), None);
}

#[test]
fn test_match_to_plan_active_low_boundary() {
    let gate = make_gate();
    let mut index = PlanIndex::new();
    index.active_plan_id = Some("active".to_string());
    assert_eq!(
        gate.match_to_plan(None, &index, &[], 0.3),
        Some("active".to_string())
    );
}

#[test]
fn test_match_to_plan_active_high_boundary() {
    let gate = make_gate();
    let mut index = PlanIndex::new();
    index.active_plan_id = Some("active".to_string());
    assert_eq!(gate.match_to_plan(None, &index, &[], 0.8), None);
}

#[test]
fn test_plan_lifecycle_simulation() {
    let gate = PlanGate::new(0.55, 3, 24);
    let mut index = PlanIndex::new();

    assert_eq!(gate.match_to_plan(None, &index, &[], 0.0), None);

    assert_eq!(
        gate.match_to_plan(Some("plan_1"), &index, &[], 0.0),
        Some("plan_1".to_string())
    );

    index.active_plan_id = Some("plan_1".to_string());

    let cur = vec![0.0_f32; 10];
    let cent = vec![0.0_f32; 10];
    let tone = make_tone(0.0, 0.5);
    let score = gate.boundary_score(
        &cur,
        &tone,
        &[],
        PlanContext { centroid: Some(&cent), avg_tone: Some(&tone), anchors: &[] },
        0.0,
    );
    assert!(score < 0.55);

    let diff: Vec<f32> = vec![1.0; 10];
    let score = gate.boundary_score(
        &diff,
        &tone,
        &[],
        PlanContext { centroid: Some(&cent), avg_tone: Some(&tone), anchors: &[] },
        0.0,
    );
    assert!(score > 0.30, "opposite vectors should yield high score, got {}", score);
}

#[test]
fn test_brain_perceive_with_plan_gate() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().to_string_lossy().to_string();
    let mut brain = memhop::Brain::open(&path, BrainConfig::default(), None).unwrap();

    let output = brain.perceive(perception("hello world", "s1")).unwrap();
    assert!(!output.engram_id.is_empty());
    assert!(!output.current_plan_id.is_empty());
    assert!(output.current_plan_id.starts_with("plan_"));
    assert_eq!(output.plan_hint, PlanHint::Continuing);
    assert_eq!(output.plan_name, "Unnamed Plan");
}

#[test]
fn test_brain_perceive_with_explicit_plan_id() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().to_string_lossy().to_string();
    let mut brain = memhop::Brain::open(&path, BrainConfig::default(), None).unwrap();

    let mut input = perception("hello world", "s1");
    input.plan_id = Some("my_plan".to_string());
    let output = brain.perceive(input).unwrap();
    assert!(!output.engram_id.is_empty());
    assert_eq!(output.current_plan_id, "my_plan");
    assert_eq!(output.plan_name, "my_plan");
}
