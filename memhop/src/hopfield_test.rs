use super::*;
use rand::Rng;
use rand::rngs::StdRng;
use rand::SeedableRng;

fn make_f16_vector(dim: usize, seed: u64) -> Vec<f16> {
    let mut rng = StdRng::seed_from_u64(seed);
    let v: Vec<f32> = (0..dim).map(|_| rng.gen_range(-1.0f32..1.0f32)).collect();
    l2_normalize_f16(&v.iter().map(|&x| f16::from_f32(x)).collect::<Vec<_>>())
}

fn to_f32_vec(v: &[f16]) -> Vec<f32> {
    v.iter().map(|x| x.to_f32()).collect()
}

#[test]
fn test_empty_recall_returns_none() {
    let mhn = ModernHopfield::new(8, 8.0);
    let query = vec![0.0f32; 8];
    assert!(mhn.recall(&query).is_none());
    assert!(mhn.recall_topk(&query, 3).is_empty());
    assert!(mhn.recall_among(&query, &["a"]).is_none());
}

#[test]
fn test_single_pattern_recall_confidence_near_one() {
    let mut mhn = ModernHopfield::new(8, 8.0);
    let pattern = make_f16_vector(8, 42);
    mhn.add_pattern("mem1", &pattern);

    let (id, confidence) = mhn.recall(&to_f32_vec(&pattern)).unwrap();
    assert_eq!(id, "mem1");
    assert!((confidence - 1.0).abs() < 1e-5, "confidence = {confidence}");
}

#[test]
fn test_orthogonal_patterns_recall_correctly() {
    let dim = 512;
    let beta = 8.0;
    let mut mhn = ModernHopfield::new(dim, beta);

    let n = 10;
    let mut patterns = Vec::with_capacity(n);
    for i in 0..n {
        let v = make_f16_vector(dim, i as u64 * 7919 + 12345);
        mhn.add_pattern(&format!("mem_{i}"), &v);
        patterns.push(v);
    }

    for (i, pattern) in patterns.iter().enumerate() {
        let (id, confidence) = mhn.recall(&to_f32_vec(pattern)).unwrap();
        assert_eq!(id, format!("mem_{i}"), "pattern {i} misidentified");
        assert!(
            confidence > 0.9,
            "pattern {i} confidence too low: {confidence}"
        );
    }
}

#[test]
fn test_remove_pattern() {
    let dim = 16;
    let mut mhn = ModernHopfield::new(dim, 8.0);

    let v1 = make_f16_vector(dim, 100);
    let v2 = make_f16_vector(dim, 200);
    let v3 = make_f16_vector(dim, 300);

    mhn.add_pattern("a", &v1);
    mhn.add_pattern("b", &v2);
    mhn.add_pattern("c", &v3);

    assert_eq!(mhn.len(), 3);

    let removed = mhn.remove_pattern("b");
    assert!(removed);
    assert_eq!(mhn.len(), 2);

    let (id, _) = mhn.recall(&to_f32_vec(&v2)).unwrap();
    assert_ne!(id, "b", "removed pattern still recalled");

    let (id_a, _) = mhn.recall(&to_f32_vec(&v1)).unwrap();
    assert_eq!(id_a, "a");

    let (id_c, _) = mhn.recall(&to_f32_vec(&v3)).unwrap();
    assert_eq!(id_c, "c");

    assert!(!mhn.remove_pattern("nonexistent"));
}

#[test]
fn test_recall_topk() {
    let dim = 16;
    let mut mhn = ModernHopfield::new(dim, 8.0);

    let v1 = make_f16_vector(dim, 1000);
    let v2 = make_f16_vector(dim, 2000);
    let v3 = make_f16_vector(dim, 3000);

    mhn.add_pattern("m1", &v1);
    mhn.add_pattern("m2", &v2);
    mhn.add_pattern("m3", &v3);

    let results = mhn.recall_topk(&to_f32_vec(&v3), 2);
    assert_eq!(results.len(), 2);
    assert_eq!(results[0].0, "m3", "top-1 should be m3");
    for window in results.windows(2) {
        assert!(
            window[0].1 >= window[1].1,
            "topk results not sorted by confidence descending"
        );
    }
}

#[test]
fn test_recall_among() {
    let dim = 16;
    let mut mhn = ModernHopfield::new(dim, 8.0);

    let v1 = make_f16_vector(dim, 111);
    let v2 = make_f16_vector(dim, 222);
    let v3 = make_f16_vector(dim, 333);

    mhn.add_pattern("x", &v1);
    mhn.add_pattern("y", &v2);
    mhn.add_pattern("z", &v3);

    let result = mhn.recall_among(&to_f32_vec(&v1), &["y", "z"]);
    assert!(result.is_some());
    let (id, _) = result.unwrap();
    assert_ne!(id, "x", "recall_among returned non-candidate");

    let result = mhn.recall_among(&to_f32_vec(&v1), &["x"]);
    let (id, conf) = result.unwrap();
    assert_eq!(id, "x");
    assert!(conf > 0.9, "single candidate confidence should be ~1.0: {conf}");

    assert!(mhn.recall_among(&to_f32_vec(&v1), &[]).is_none());
    assert!(mhn.recall_among(&to_f32_vec(&v1), &["nonexistent"]).is_none());
}

#[test]
fn test_add_pattern_replaces_existing() {
    let dim = 8;
    let mut mhn = ModernHopfield::new(dim, 8.0);

    let v1 = make_f16_vector(dim, 50);
    let v2 = make_f16_vector(dim, 99);

    mhn.add_pattern("id1", &v1);
    assert_eq!(mhn.len(), 1);

    mhn.add_pattern("id1", &v2);
    assert_eq!(mhn.len(), 1, "replace should not increase count");

    let (id, conf) = mhn.recall(&to_f32_vec(&v2)).unwrap();
    assert_eq!(id, "id1");
    assert!(conf > 0.99, "after replace, confidence should be ~1.0: {conf}");
}

#[test]
fn test_is_empty() {
    let mut mhn = ModernHopfield::new(4, 8.0);
    assert!(mhn.is_empty());

    let v = vec![f16::from_f32(1.0), f16::ZERO, f16::ZERO, f16::ZERO];
    mhn.add_pattern("a", &v);
    assert!(!mhn.is_empty());

    mhn.remove_pattern("a");
    assert!(mhn.is_empty());
}

#[test]
fn test_swap_remove_consistency() {
    let dim = 8;
    let mut mhn = ModernHopfield::new(dim, 8.0);

    let vs: Vec<Vec<f16>> = (0..5).map(|i| make_f16_vector(dim, i * 7777)).collect();
    for (i, v) in vs.iter().enumerate() {
        mhn.add_pattern(&format!("p{i}"), v);
    }

    mhn.remove_pattern("p2");
    assert_eq!(mhn.len(), 4);

    for (i, v) in vs.iter().enumerate() {
        let id_str = format!("p{i}");
        if id_str == "p2" {
            continue;
        }
        let (id, conf) = mhn.recall(&to_f32_vec(v)).unwrap();
        assert_eq!(id, id_str, "after remove, pattern {id_str} misidentified as {id}");
        assert!(conf > 0.9, "after remove, confidence for {id_str} too low: {conf}");
    }

    mhn.remove_pattern("p0");
    mhn.remove_pattern("p4");
    assert_eq!(mhn.len(), 2);

    let (id, _) = mhn.recall(&to_f32_vec(&vs[1])).unwrap();
    assert_eq!(id, "p1");
    let (id, _) = mhn.recall(&to_f32_vec(&vs[3])).unwrap();
    assert_eq!(id, "p3");
}

// ── v0.4.0 plasticity tests ────────────────────────────

#[test]
fn test_drift_disabled_equals_recall() {
    let dim = 64;
    let mut mhn = ModernHopfield::new(dim, 8.0);

    for i in 0..5 {
        let v = make_f16_vector(dim, i * 131);
        mhn.add_pattern(&format!("m{i}"), &v);
    }

    let query = make_f16_vector(dim, 42);
    let query_f32 = to_f32_vec(&query);

    // drift_enabled is false by default
    let (id1, conf1) = mhn.recall(&query_f32).unwrap();
    let (id2, conf2, _drifted) = mhn.recall_with_plasticity(&query_f32, 0).unwrap();

    assert_eq!(id1, id2);
    assert!((conf1 - conf2).abs() < 1e-5);
}

#[test]
fn test_winner_reinforcement() {
    let dim = 128;
    let mut mhn = ModernHopfield::new(dim, 8.0);
    mhn.enable_plasticity(true);

    // Add a close pattern and distant patterns
    let target = make_f16_vector(dim, 1000);
    let target_f32 = to_f32_vec(&target);
    mhn.add_pattern("target", &target);

    for i in 1..5 {
        let v = make_f16_vector(dim, 1000 + i * 7777);
        mhn.add_pattern(&format!("dist{i}"), &v);
    }

    // Record similarity before drift
    let (_, conf_before) = mhn.recall(&target_f32).unwrap();

    // Drift toward target
    mhn.recall_with_plasticity(&target_f32, 1000);

    // After drift, target should be even closer
    let (id_after, conf_after) = mhn.recall(&target_f32).unwrap();
    assert_eq!(id_after, "target");
    assert!(
        conf_after >= conf_before - 0.01,
        "winner confidence should not decrease: before={conf_before}, after={conf_after}"
    );
}

#[test]
fn test_access_counts_increment() {
    let dim = 32;
    let mut mhn = ModernHopfield::new(dim, 8.0);
    mhn.enable_plasticity(true);

    let v = make_f16_vector(dim, 42);
    let q = to_f32_vec(&v);
    mhn.add_pattern("a", &v);

    let (id, _, _) = mhn.recall_with_plasticity(&q, 5000).unwrap();
    assert_eq!(id, "a");

    let (count, last_access) = mhn.get_access_stats("a").unwrap();
    assert_eq!(count, 1);
    assert_eq!(last_access, 5000);
}

#[test]
fn test_get_access_stats_nonexistent() {
    let mhn = ModernHopfield::new(16, 8.0);
    assert!(mhn.get_access_stats("nonexistent").is_none());
}

#[test]
fn test_enable_plasticity_toggle() {
    let mut mhn = ModernHopfield::new(8, 8.0);
    assert!(!mhn.drift_enabled, "default should be disabled");

    mhn.enable_plasticity(true);
    assert!(mhn.drift_enabled);

    mhn.enable_plasticity(false);
    assert!(!mhn.drift_enabled);
}

#[test]
fn test_set_plasticity_config() {
    let mut mhn = ModernHopfield::new(8, 8.0);
    let mut cfg = PlasticityConfig::default();
    cfg.reinforce_rate = 0.02;
    cfg.discriminate_rate = 0.01;

    mhn.set_plasticity_config(cfg.clone());

    assert!((mhn.plasticity_cfg.reinforce_rate - 0.02).abs() < 1e-6);
    assert!((mhn.plasticity_cfg.discriminate_rate - 0.01).abs() < 1e-6);
}

#[test]
fn test_apply_decay_triggers_after_threshold() {
    let dim = 16;
    let mut mhn = ModernHopfield::new(dim, 8.0);

    let v = make_f16_vector(dim, 42);
    mhn.add_pattern("old_mem", &v);

    // Set a low decay threshold so decay kicks in quickly
    mhn.plasticity_cfg.decay_threshold_days = 1;
    mhn.plasticity_cfg.decay_rate = 0.5;

    // Mark as accessed in the past
    mhn.last_access[0] = 0;  // Never accessed via plasticity

    // apply_decay skips patterns with last_access=0
    let dormant = mhn.apply_decay(1000000);
    assert!(dormant.is_empty(), "patterns with last_access=0 should not decay");

    // Now set last_access in the past
    let past = 1000; // 1000 ms after epoch = way in the past
    mhn.last_access[0] = past;

    let _dormant = mhn.apply_decay(past + 3 * 86400000); // 3 days after past
    // Pattern should have decayed
    let decayed_norm = l2_norm_f16(&mhn.patterns[0..dim]);
    assert!(
        decayed_norm < 1.0,
        "decayed pattern should have lower norm: {decayed_norm}"
    );
}

#[test]
fn test_remove_pattern_maintains_access_stats() {
    let dim = 8;
    let mut mhn = ModernHopfield::new(dim, 8.0);

    let v1 = make_f16_vector(dim, 10);
    let v2 = make_f16_vector(dim, 20);
    let v3 = make_f16_vector(dim, 30);

    mhn.add_pattern("a", &v1);
    mhn.add_pattern("b", &v2);
    mhn.add_pattern("c", &v3);

    mhn.access_counts[0] = 5;
    mhn.last_access[0] = 100;
    mhn.access_counts[1] = 3;
    mhn.last_access[1] = 200;

    // Remove middle element
    mhn.remove_pattern("b");

    // "a" should still have its stats
    let (count, access) = mhn.get_access_stats("a").unwrap();
    assert_eq!(count, 5);
    assert_eq!(access, 100);

    // "c" should still have its stats
    let (count, access) = mhn.get_access_stats("c").unwrap();
    assert_eq!(count, 0);
    assert_eq!(access, 0);

    assert_eq!(mhn.access_counts.len(), 2);
    assert_eq!(mhn.last_access.len(), 2);
}
