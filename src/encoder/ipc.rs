// Encoder IPC interface (reserved for v0.31+)

use half::f16;
use std::collections::HashMap;
use std::io::{Read, Write};
use std::os::unix::net::UnixStream;
use std::path::PathBuf;
use std::time::Duration;

use crate::MemHopError;

/// Encoder trait for external encoding service
pub trait Encoder: Send + Sync {
    /// Encode text to dense and sparse vectors
    fn encode(&self, text: &str) -> EncoderOutput;

    /// Get vector dimension
    fn dim(&self) -> usize;

    /// Get encoder mode (e.g., "dense", "sparse", "hybrid")
    fn mode(&self) -> &str;
}

/// Output from encoder
pub struct EncoderOutput {
    pub dense: Vec<f16>,
    pub sparse: HashMap<String, f32>,
}

/// IPC-based encoder for connecting to external encoding service
#[allow(dead_code)]
pub struct IpcEncoder {
    socket_path: PathBuf,
    dim: usize,
    mode: String,
}

impl IpcEncoder {
    /// Create new IPC encoder
    pub fn new(socket_path: PathBuf, dim: usize, mode: String) -> Self {
        Self {
            socket_path,
            dim,
            mode,
        }
    }

    /// Check if the encoder socket is available
    ///
    /// Attempts to connect to the Unix socket.
    /// Returns true if connection succeeds, false otherwise.
    pub fn is_available(&self) -> bool {
        UnixStream::connect(&self.socket_path).is_ok()
    }

    /// Try to encode text via Unix socket communication
    ///
    /// # Protocol
    /// Request: [text_len: u32 LE][text bytes]
    /// Response: [dim: u16 LE][dense: f16 x dim][sparse_count: u32][key_len(u16) + key + weight(f32)] x N
    ///
    /// # Errors
    /// Returns MemHopError if:
    /// - Socket connection fails
    /// - IO operations fail
    /// - UTF-8 decoding fails
    fn try_encode(&self, text: &str) -> Result<EncoderOutput, MemHopError> {
        // 1. Connect to Unix socket
        let mut stream = UnixStream::connect(&self.socket_path)
            .map_err(MemHopError::Io)?;

        // Set timeouts
        stream.set_read_timeout(Some(Duration::from_secs(5)))
            .map_err(MemHopError::Io)?;
        stream.set_write_timeout(Some(Duration::from_secs(2)))
            .map_err(MemHopError::Io)?;

        // 2. Send request: [text_len: u32 LE][text bytes]
        let text_bytes = text.as_bytes();
        stream.write_all(&(text_bytes.len() as u32).to_le_bytes())?;
        stream.write_all(text_bytes)?;

        // 3. Receive response: [dim: u16 LE][dense: f16 × dim][sparse_count: u32]...
        let mut dim_buf = [0u8; 2];
        stream.read_exact(&mut dim_buf)?;
        let dim = u16::from_le_bytes(dim_buf) as usize;

        let mut dense = Vec::with_capacity(dim);
        for _ in 0..dim {
            let mut f16_buf = [0u8; 2];
            stream.read_exact(&mut f16_buf)?;
            dense.push(f16::from_le_bytes(f16_buf));
        }

        let mut sparse_count_buf = [0u8; 4];
        stream.read_exact(&mut sparse_count_buf)?;
        let sparse_count = u32::from_le_bytes(sparse_count_buf) as usize;

        let mut sparse = HashMap::new();
        for _ in 0..sparse_count {
            let mut key_len_buf = [0u8; 2];
            stream.read_exact(&mut key_len_buf)?;
            let key_len = u16::from_le_bytes(key_len_buf) as usize;

            let mut key_buf = vec![0u8; key_len];
            stream.read_exact(&mut key_buf)?;
            let key = String::from_utf8(key_buf)
                .map_err(|e| MemHopError::Serialization(e.to_string()))?;

            let mut weight_buf = [0u8; 4];
            stream.read_exact(&mut weight_buf)?;
            let weight = f32::from_le_bytes(weight_buf);

            sparse.insert(key, weight);
        }

        Ok(EncoderOutput { dense, sparse })
    }
}

impl Encoder for IpcEncoder {
    fn encode(&self, text: &str) -> EncoderOutput {
        match self.try_encode(text) {
            Ok(output) => output,
            Err(e) => {
                eprintln!("IPC encode failed: {:?}, returning zero vector", e);
                EncoderOutput {
                    dense: vec![f16::from_f32(0.0); self.dim],
                    sparse: HashMap::new(),
                }
            }
        }
    }

    fn dim(&self) -> usize {
        self.dim
    }

    fn mode(&self) -> &str {
        &self.mode
    }
}

/// Mock encoder for testing (no IPC required)
pub struct MockEncoder {
    dim: usize,
    mode: String,
}

impl MockEncoder {
    /// Create new mock encoder
    pub fn new(dim: usize) -> Self {
        Self {
            dim,
            mode: "mock".to_string(),
        }
    }
}

/// Shared mock encoding logic — generates deterministic vectors from text content.
/// Used by both MockEncoder and IpcEncoder fallback.
fn mock_encode(text: &str, dim: usize) -> EncoderOutput {
    // Use content hash for deterministic, content-sensitive vectors
    let mut hash: u64 = 0xcbf29ce484222325; // FNV offset basis
    for byte in text.bytes() {
        hash ^= byte as u64;
        hash = hash.wrapping_mul(0x100000001b3); // FNV prime
    }
    let hash_f = (hash & 0xFFFF) as f32;

    let dense = (0..dim)
        .map(|i| f16::from_f32((hash_f + i as f32) / (dim as f32)))
        .collect();

    let mut sparse = HashMap::new();
    for word in text.split_whitespace() {
        sparse.insert(word.to_lowercase(), 1.0);
    }

    EncoderOutput { dense, sparse }
}

impl Encoder for MockEncoder {
    fn encode(&self, text: &str) -> EncoderOutput {
        mock_encode(text, self.dim)
    }

    fn dim(&self) -> usize {
        self.dim
    }

    fn mode(&self) -> &str {
        &self.mode
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_mock_encoder_basic() {
        let encoder = MockEncoder::new(768);
        let output = encoder.encode("hello world");

        assert_eq!(output.dense.len(), 768);
        assert_eq!(output.sparse.len(), 2); // "hello" and "world"
        assert!(output.sparse.contains_key("hello"));
        assert!(output.sparse.contains_key("world"));
    }

    #[test]
    fn test_mock_encoder_dim() {
        let encoder = MockEncoder::new(512);
        assert_eq!(encoder.dim(), 512);

        let output = encoder.encode("test");
        assert_eq!(output.dense.len(), 512);
    }

    #[test]
    fn test_mock_encoder_mode() {
        let encoder = MockEncoder::new(768);
        assert_eq!(encoder.mode(), "mock");
    }

    #[test]
    fn test_ipc_encoder_placeholder() {
        let encoder = IpcEncoder::new(PathBuf::from("/tmp/test.sock"), 768, "dense".to_string());

        assert_eq!(encoder.dim(), 768);
        assert_eq!(encoder.mode(), "dense");

        // Should return zeros when socket is unavailable
        let output = encoder.encode("test");
        assert_eq!(output.dense.len(), 768);
        assert!(output.dense.iter().all(|&x| x == f16::from_f32(0.0)));
    }
    
    #[test]
    fn test_ipc_encoder_fallback_on_connection_failure() {
        // Test that encode gracefully handles connection failures
        let encoder = IpcEncoder::new(
            PathBuf::from("/tmp/nonexistent_socket_12345.sock"),
            768,
            "dense".to_string(),
        );
        
        // Should not panic, should return zero vector
        let output = encoder.encode("test text");
        assert_eq!(output.dense.len(), 768);
        assert!(output.sparse.is_empty());
        assert!(output.dense.iter().all(|&x| x == f16::from_f32(0.0)));
    }
}
