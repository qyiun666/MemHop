// Common I/O helpers for slot serialization/deserialization
//
// These functions eliminate redundant closure definitions across slot files.

use std::io::{self, Cursor, Read, Write};

// --- Read helpers ---

/// Read u8 from cursor
#[inline]
pub fn read_u8(cursor: &mut Cursor<&[u8]>) -> io::Result<u8> {
    let mut buf = [0u8; 1];
    cursor.read_exact(&mut buf)?;
    Ok(buf[0])
}

/// Read u16 (LE) from cursor
#[inline]
pub fn read_u16(cursor: &mut Cursor<&[u8]>) -> io::Result<u16> {
    let mut buf = [0u8; 2];
    cursor.read_exact(&mut buf)?;
    Ok(u16::from_le_bytes(buf))
}

/// Read u32 (LE) from cursor
#[inline]
pub fn read_u32(cursor: &mut Cursor<&[u8]>) -> io::Result<u32> {
    let mut buf = [0u8; 4];
    cursor.read_exact(&mut buf)?;
    Ok(u32::from_le_bytes(buf))
}

/// Read u64 (LE) from cursor
#[inline]
pub fn read_u64(cursor: &mut Cursor<&[u8]>) -> io::Result<u64> {
    let mut buf = [0u8; 8];
    cursor.read_exact(&mut buf)?;
    Ok(u64::from_le_bytes(buf))
}

/// Read i64 (LE) from cursor
#[inline]
pub fn read_i64(cursor: &mut Cursor<&[u8]>) -> io::Result<i64> {
    let mut buf = [0u8; 8];
    cursor.read_exact(&mut buf)?;
    Ok(i64::from_le_bytes(buf))
}

/// Read f32 (LE) from cursor
#[inline]
pub fn read_f32(cursor: &mut Cursor<&[u8]>) -> io::Result<f32> {
    let mut buf = [0u8; 4];
    cursor.read_exact(&mut buf)?;
    Ok(f32::from_le_bytes(buf))
}

/// Read length-prefixed UTF-8 string from cursor
///
/// Format: [u16 length][bytes]
pub fn read_string(cursor: &mut Cursor<&[u8]>) -> io::Result<String> {
    let len = read_u16(cursor)? as usize;
    let mut buf = vec![0u8; len];
    cursor.read_exact(&mut buf)?;
    String::from_utf8(buf).map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))
}

/// Read optional length-prefixed UTF-8 string from cursor
///
/// Returns None if length is 0.
pub fn read_optional_string(cursor: &mut Cursor<&[u8]>) -> io::Result<Option<String>> {
    let len = read_u16(cursor)? as usize;
    if len == 0 {
        return Ok(None);
    }
    let mut buf = vec![0u8; len];
    cursor.read_exact(&mut buf)?;
    Ok(Some(String::from_utf8(buf).map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?))
}

/// Read vector of length-prefixed strings from cursor
pub fn read_string_vec(cursor: &mut Cursor<&[u8]>) -> io::Result<Vec<String>> {
    let count = read_u16(cursor)? as usize;
    let mut vec = Vec::with_capacity(count);
    for _ in 0..count {
        vec.push(read_string(cursor)?);
    }
    Ok(vec)
}

// --- Write helpers ---

/// Write length-prefixed UTF-8 string to buffer
///
/// Format: [u16 length][bytes]
pub fn write_string(buffer: &mut Vec<u8>, s: &str) -> io::Result<()> {
    let len = s.len() as u16;
    buffer.write_all(&len.to_le_bytes())?;
    buffer.write_all(s.as_bytes())?;
    Ok(())
}

/// Write optional length-prefixed UTF-8 string to buffer
///
/// Writes 0 length prefix if None.
pub fn write_optional_string(buffer: &mut Vec<u8>, s: &Option<String>) -> io::Result<()> {
    match s {
        Some(s) => write_string(buffer, s),
        None => {
            buffer.write_all(&0u16.to_le_bytes())?;
            Ok(())
        }
    }
}

/// Write vector of length-prefixed strings to buffer
pub fn write_string_vec(buffer: &mut Vec<u8>, vec: &[String]) -> io::Result<()> {
    let count = vec.len() as u16;
    buffer.write_all(&count.to_le_bytes())?;
    for s in vec {
        write_string(buffer, s)?;
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_read_write_string() {
        let mut buffer = Vec::new();
        write_string(&mut buffer, "hello world").unwrap();

        let mut cursor = Cursor::new(buffer.as_slice());
        let result = read_string(&mut cursor).unwrap();
        assert_eq!(result, "hello world");
    }

    #[test]
    fn test_read_write_optional_string_some() {
        let mut buffer = Vec::new();
        write_optional_string(&mut buffer, &Some("test".to_string())).unwrap();

        let mut cursor = Cursor::new(buffer.as_slice());
        let result = read_optional_string(&mut cursor).unwrap();
        assert_eq!(result, Some("test".to_string()));
    }

    #[test]
    fn test_read_write_optional_string_none() {
        let mut buffer = Vec::new();
        write_optional_string(&mut buffer, &None).unwrap();

        let mut cursor = Cursor::new(buffer.as_slice());
        let result = read_optional_string(&mut cursor).unwrap();
        assert_eq!(result, None);
    }

    #[test]
    fn test_read_write_string_vec() {
        let mut buffer = Vec::new();
        let vec = vec!["a".to_string(), "bc".to_string(), "def".to_string()];
        write_string_vec(&mut buffer, &vec).unwrap();

        let mut cursor = Cursor::new(buffer.as_slice());
        let result = read_string_vec(&mut cursor).unwrap();
        assert_eq!(result, vec);
    }

    #[test]
    #[allow(clippy::approx_constant)]
    fn test_read_write_numeric() {
        let mut buffer = Vec::new();
        buffer.write_all(&42u8.to_le_bytes()).unwrap();
        buffer.write_all(&1234u16.to_le_bytes()).unwrap();
        buffer.write_all(&56789u32.to_le_bytes()).unwrap();
        buffer.write_all(&123456789u64.to_le_bytes()).unwrap();
        buffer.write_all(&(-987654321i64).to_le_bytes()).unwrap();
        buffer.write_all(&3.14f32.to_le_bytes()).unwrap();

        let mut cursor = Cursor::new(buffer.as_slice());
        assert_eq!(read_u8(&mut cursor).unwrap(), 42);
        assert_eq!(read_u16(&mut cursor).unwrap(), 1234);
        assert_eq!(read_u32(&mut cursor).unwrap(), 56789);
        assert_eq!(read_u64(&mut cursor).unwrap(), 123456789);
        assert_eq!(read_i64(&mut cursor).unwrap(), -987654321);
        assert!((read_f32(&mut cursor).unwrap() - 3.14).abs() < 0.001);
    }
}
