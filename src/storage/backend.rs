// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

//! Storage backend abstraction: mmap for native, buffered for WASM.
//!
//! `StorageBackend` trait, `MmapBackend`, and `BufferedBackend` are only
//! reachable via tests or WASM builds after the v2 engine rewrite.
//! They are gated behind `#[cfg(any(test, target_arch = "wasm32"))]`.

#[cfg(any(test, target_arch = "wasm32"))]
use crate::{MemHopError, Result};
#[cfg(any(test, target_arch = "wasm32"))]
use std::fs::File;
#[cfg(any(test, target_arch = "wasm32"))]
use std::io::{Seek, SeekFrom, Write};

/// Trait for low-level storage backends.
#[cfg(any(test, target_arch = "wasm32"))]
pub trait StorageBackend: Send + Sync {
    fn read(&self, offset: u64, len: usize) -> Result<&[u8]>;
    fn append(&mut self, data: &[u8]) -> Result<u64>;
    fn sync(&self) -> Result<()>;
    fn len(&self) -> u64;
    fn is_empty(&self) -> bool {
        self.len() == 0
    }
}

#[cfg(any(test, target_arch = "wasm32"))]
#[cfg(not(target_arch = "wasm32"))]
pub struct MmapBackend {
    file: File,
    mmap: memmap2::MmapMut,
}

#[cfg(any(test, target_arch = "wasm32"))]
#[cfg(not(target_arch = "wasm32"))]
impl MmapBackend {
    pub fn new(file: File) -> Result<Self> {
        let mmap = unsafe { memmap2::MmapMut::map_mut(&file)? };
        Ok(Self { file, mmap })
    }

    fn refresh_mmap(&mut self) -> Result<()> {
        self.mmap = unsafe { memmap2::MmapMut::map_mut(&self.file)? };
        Ok(())
    }
}

#[cfg(any(test, target_arch = "wasm32"))]
#[cfg(not(target_arch = "wasm32"))]
impl StorageBackend for MmapBackend {
    fn read(&self, offset: u64, len: usize) -> Result<&[u8]> {
        let start = offset as usize;
        let end = start + len;
        if end > self.mmap.len() {
            return Err(MemHopError::Io(std::io::Error::new(
                std::io::ErrorKind::UnexpectedEof,
                "read beyond mmap length",
            )));
        }
        Ok(&self.mmap[start..end])
    }

    fn append(&mut self, data: &[u8]) -> Result<u64> {
        let offset = self.file.seek(SeekFrom::End(0))?;
        self.file.write_all(data)?;
        self.file.flush()?;
        self.refresh_mmap()?;
        Ok(offset)
    }

    fn sync(&self) -> Result<()> {
        self.mmap.flush()?;
        self.file.sync_all()?;
        Ok(())
    }

    fn len(&self) -> u64 {
        self.mmap.len() as u64
    }
}

#[cfg(any(test, target_arch = "wasm32"))]
#[cfg(target_arch = "wasm32")]
pub struct BufferedBackend {
    data: Vec<u8>,
}

#[cfg(any(test, target_arch = "wasm32"))]
#[cfg(target_arch = "wasm32")]
impl BufferedBackend {
    pub fn new() -> Self {
        Self { data: Vec::new() }
    }
}

#[cfg(any(test, target_arch = "wasm32"))]
#[cfg(target_arch = "wasm32")]
impl StorageBackend for BufferedBackend {
    fn read(&self, offset: u64, len: usize) -> Result<&[u8]> {
        let start = offset as usize;
        let end = start + len;
        if end > self.data.len() {
            return Err(MemHopError::Io(std::io::Error::new(
                std::io::ErrorKind::UnexpectedEof,
                "read beyond buffer length",
            )));
        }
        Ok(&self.data[start..end])
    }

    fn append(&mut self, data: &[u8]) -> Result<u64> {
        let offset = self.data.len() as u64;
        self.data.extend_from_slice(data);
        Ok(offset)
    }

    fn sync(&self) -> Result<()> {
        Ok(())
    }

    fn len(&self) -> u64 {
        self.data.len() as u64
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::NamedTempFile;

    #[test]
    fn test_mmap_backend_roundtrip() {
        let temp = NamedTempFile::new().unwrap();
        let file = File::options()
            .read(true)
            .write(true)
            .create(true)
            .truncate(true)
            .open(temp.path())
            .unwrap();

        let mut backend = MmapBackend::new(file).unwrap();
        assert!(backend.is_empty());

        let data = b"hello mmap backend";
        let offset = backend.append(data).unwrap();
        assert_eq!(offset, 0);
        assert_eq!(backend.len(), data.len() as u64);

        let read = backend.read(offset, data.len()).unwrap();
        assert_eq!(read, data.as_slice());
    }

    #[test]
    fn test_mmap_backend_multiple_appends() {
        let temp = NamedTempFile::new().unwrap();
        let file = File::options()
            .read(true)
            .write(true)
            .create(true)
            .truncate(true)
            .open(temp.path())
            .unwrap();

        let mut backend = MmapBackend::new(file).unwrap();
        let off1 = backend.append(b"first").unwrap();
        let off2 = backend.append(b"second").unwrap();

        assert_eq!(off1, 0);
        assert_eq!(off2, 5);

        assert_eq!(backend.read(off1, 5).unwrap(), b"first");
        assert_eq!(backend.read(off2, 6).unwrap(), b"second");
    }

    #[test]
    fn test_mmap_backend_read_beyond_end() {
        let temp = NamedTempFile::new().unwrap();
        let file = File::options()
            .read(true)
            .write(true)
            .create(true)
            .truncate(true)
            .open(temp.path())
            .unwrap();

        let backend = MmapBackend::new(file).unwrap();
        assert!(backend.read(0, 1).is_err());
    }
}
