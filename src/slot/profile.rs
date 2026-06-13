// L0 ProfileSlot - agent self-portrait (JSON format)
//
// Stores agent identity: name, role, personality, values, worldview.
// Uses JSON serialization for human readability and flexibility.

use std::collections::HashMap;
use std::io;

/// L0 Agent profile - defines who the agent is
#[derive(Debug, Clone, PartialEq)]
pub struct ProfileSlot {
    pub id_hash: u64,
    pub name: String,
    pub role: String,
    pub personality: String,
    pub values: String,
    pub worldview: String,
    pub preferences: HashMap<String, String>,
    pub created_at: i64,
    pub updated_at: i64,
    pub version: u32,
}

/// JSON representation for serialization
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
struct ProfileJson {
    id_hash: u64,
    name: String,
    role: String,
    personality: String,
    values: String,
    worldview: String,
    preferences: HashMap<String, String>,
    created_at: i64,
    updated_at: i64,
    version: u32,
}

impl ProfileSlot {
    /// Serialize to JSON bytes
    pub fn serialize(&self) -> io::Result<Vec<u8>> {
        let json = ProfileJson {
            id_hash: self.id_hash,
            name: self.name.clone(),
            role: self.role.clone(),
            personality: self.personality.clone(),
            values: self.values.clone(),
            worldview: self.worldview.clone(),
            preferences: self.preferences.clone(),
            created_at: self.created_at,
            updated_at: self.updated_at,
            version: self.version,
        };
        serde_json::to_vec(&json).map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))
    }

    /// Deserialize from JSON bytes
    pub fn deserialize(data: &[u8]) -> io::Result<Self> {
        // Find the end of JSON by looking for the last '}'
        // This handles cases where data contains trailing zeros or garbage
        let mut brace_count = 0i32;
        let mut json_end = data.len();
        
        for (i, &byte) in data.iter().enumerate() {
            if byte == b'{' {
                brace_count += 1;
            } else if byte == b'}' {
                brace_count -= 1;
                if brace_count == 0 {
                    json_end = i + 1;
                    break;
                }
            } else if byte == 0 && brace_count == 0 {
                // Hit null byte before finding JSON end
                return Err(io::Error::new(io::ErrorKind::InvalidData, "Invalid JSON data"));
            }
        }
        
        let json_data = &data[..json_end];
        let json: ProfileJson = serde_json::from_slice(json_data)
            .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;
        Ok(ProfileSlot {
            id_hash: json.id_hash,
            name: json.name,
            role: json.role,
            personality: json.personality,
            values: json.values,
            worldview: json.worldview,
            preferences: json.preferences,
            created_at: json.created_at,
            updated_at: json.updated_at,
            version: json.version,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_profile_roundtrip() {
        let mut preferences = HashMap::new();
        preferences.insert("language".to_string(), "Rust".to_string());
        preferences.insert("style".to_string(), "concise".to_string());
        
        let profile = ProfileSlot {
            id_hash: 1,
            name: "Meow".to_string(),
            role: "assistant".to_string(),
            personality: "friendly, helpful".to_string(),
            values: "honesty, curiosity".to_string(),
            worldview: "knowledge should be accessible".to_string(),
            preferences,
            created_at: 1000,
            updated_at: 2000,
            version: 1,
        };
        let data = profile.serialize().unwrap();
        let deserialized = ProfileSlot::deserialize(&data).unwrap();
        assert_eq!(profile, deserialized);
    }

    #[test]
    fn test_profile_json_readable() {
        let profile = ProfileSlot {
            id_hash: 1,
            name: "Test".to_string(),
            role: "agent".to_string(),
            personality: "calm".to_string(),
            values: "truth".to_string(),
            worldview: "neutral".to_string(),
            preferences: HashMap::new(),
            created_at: 0,
            updated_at: 0,
            version: 0,
        };
        let data = profile.serialize().unwrap();
        let json_str = String::from_utf8(data).unwrap();
        assert!(json_str.contains("\"name\":\"Test\""));
    }
}
