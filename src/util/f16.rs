// F16 utility module
// Helper functions for converting between f16 and f32

/// Convert f16 bytes (little-endian) to f32
pub fn from_le_bytes(bytes: [u8; 2]) -> f32 {
    let bits = u16::from_le_bytes(bytes);
    f16_to_f32(bits)
}

/// Convert f16 bit representation to f32
fn f16_to_f32(bits: u16) -> f32 {
    let sign = ((bits >> 15) & 1) as i32;
    let exponent = ((bits >> 10) & 0x1F) as i32;
    let mantissa = (bits & 0x3FF) as i32;

    if exponent == 0 {
        if mantissa == 0 {
            // Zero
            return if sign == 0 { 0.0 } else { -0.0 };
        } else {
            // Subnormal number
            let value = (mantissa as f32) / 1024.0;
            let result = value * 2.0f32.powi(-14);
            return if sign == 0 { result } else { -result };
        }
    } else if exponent == 31 {
        // Infinity or NaN
        if mantissa == 0 {
            return if sign == 0 {
                f32::INFINITY
            } else {
                f32::NEG_INFINITY
            };
        } else {
            return f32::NAN;
        }
    }

    // Normal number
    let exponent = exponent - 15;
    let mantissa = mantissa | 1024; // Add implicit leading 1
    let value = (mantissa as f32) / 1024.0;
    let result = value * 2.0f32.powi(exponent);

    if sign == 0 {
        result
    } else {
        -result
    }
}
