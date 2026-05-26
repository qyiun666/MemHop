//! Personality and GrowthState for the Brain.

use serde::{Deserialize, Serialize};

// ── Personality ───────────────────────────────────────────────

/// Three-parameter personality model affecting decay, spread, and emotional behaviour.
#[derive(Debug, Clone, Copy, Serialize, Deserialize)]
pub struct Personality {
    /// 0~1. Higher → stronger emotional protection against decay.
    pub emotional_sensitivity: f32,
    /// 0~1. Higher → faster vitality decay.
    pub forgetfulness: f32,
    /// 0~1. Higher → broader spreading activation.
    pub associative_breadth: f32,
}

impl Default for Personality {
    fn default() -> Self {
        Personality {
            emotional_sensitivity: 0.5,
            forgetfulness: 0.5,
            associative_breadth: 0.5,
        }
    }
}

impl Personality {
    /// Time-decay rate: base + forgetfulness contribution.
    pub fn decay_lambda(&self) -> f32 {
        0.01 + self.forgetfulness * 0.03
    }

    /// Interference decay factor: base + forgetfulness contribution.
    pub fn interference_alpha(&self) -> f32 {
        0.05 + self.forgetfulness * 0.1
    }

    /// Arousal protection beta: base + emotional_sensitivity contribution.
    pub fn arousal_beta(&self) -> f32 {
        0.1 + self.emotional_sensitivity * 0.4
    }

    /// Spread top-K: base + associative_breadth.
    pub fn spread_top_k(&self) -> usize {
        5 + (self.associative_breadth * 10.0) as usize
    }

    /// Spread depth: 1 + associative_breadth * 2 (rounded).
    pub fn spread_depth(&self) -> usize {
        1 + (self.associative_breadth * 2.0).round() as usize
    }

    /// Contradiction inhibition strength: base + associative_breadth contribution.
    pub fn contradiction_inhibition(&self) -> f32 {
        0.3 + self.associative_breadth * 0.4
    }
}

// ── GrowthState ───────────────────────────────────────────────

/// Tracks the Brain's growth and lifecycle statistics.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct GrowthState {
    /// Total number of perceive() calls ever made.
    pub total_perceptions: u64,
    /// Total number of recall() calls ever made.
    pub total_recalls: u64,
    /// Total number of reflect() calls ever made.
    pub total_reflections: u64,
    /// Total number of Dream cycles completed.
    pub dream_cycles: u64,
    /// Total number of engrams ever created (including forgotten ones).
    pub total_engrams_created: u64,
    /// Total number of engrams consolidated from hippocampus to neocortex.
    pub total_consolidated: u64,
    /// Total number of engrams forgotten (deleted).
    pub total_forgotten: u64,
    /// Total number of schemas emerged.
    pub total_schemas_emerged: u64,
    /// Total number of contradictions detected.
    pub total_contradictions: u64,
}

impl GrowthState {
    pub fn new() -> Self {
        GrowthState {
            total_perceptions: 0,
            total_recalls: 0,
            total_reflections: 0,
            dream_cycles: 0,
            total_engrams_created: 0,
            total_consolidated: 0,
            total_forgotten: 0,
            total_schemas_emerged: 0,
            total_contradictions: 0,
        }
    }
}

impl Default for GrowthState {
    fn default() -> Self {
        Self::new()
    }
}
