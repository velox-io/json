//! JSON scanner: low-level tokenization primitives.
//!
//! All functions take `(src, &mut idx)`, advance idx past consumed bytes,
//! and return `ScanResult`. Streaming-aware: numbers at buffer boundary
//! return Eof (may continue in next chunk).

#[derive(Debug, Clone, Copy, PartialEq)]
pub enum ScanResult<T> {
    Ok(T),
    Eof,
    Error(usize),
}

#[inline]
pub fn skip_whitespace(src: &[u8], idx: &mut usize) {
    while *idx < src.len() {
        match src[*idx] {
            b' ' | b'\t' | b'\r' | b'\n' => *idx += 1,
            _ => return,
        }
    }
}

#[inline]
pub fn expect_byte(src: &[u8], idx: &mut usize, b: u8) -> ScanResult<()> {
    if *idx >= src.len() {
        return ScanResult::Eof;
    }
    if src[*idx] == b {
        *idx += 1;
        ScanResult::Ok(())
    } else {
        ScanResult::Error(*idx)
    }
}

/// Scan a JSON string body (opening '"' already consumed).
/// Returns (content_start, content_end, has_escape).
pub fn scan_string(src: &[u8], idx: &mut usize) -> ScanResult<(usize, usize, bool)> {
    let start = *idx;
    let mut has_escape = false;
    while *idx < src.len() {
        match src[*idx] {
            b'"' => {
                let end = *idx;
                *idx += 1; // skip closing quote
                return ScanResult::Ok((start, end, has_escape));
            }
            b'\\' => {
                has_escape = true;
                *idx += 1; // skip backslash
                if *idx >= src.len() {
                    return ScanResult::Eof;
                }
                *idx += 1; // skip escaped char
            }
            _ => *idx += 1,
        }
    }
    ScanResult::Eof
}

/// Parse a JSON signed integer. Streaming-aware: Eof if digits reach
/// end of src (number may continue in next buffer).
pub fn parse_int64(src: &[u8], idx: &mut usize) -> ScanResult<i64> {
    if *idx >= src.len() {
        return ScanResult::Eof;
    }

    let save = *idx;

    let negative = src[*idx] == b'-';
    if negative {
        *idx += 1;
        if *idx >= src.len() {
            *idx = save;
            return ScanResult::Eof;
        }
    }

    let start = *idx;
    let mut value: i64 = 0;

    while *idx < src.len() {
        let b = src[*idx];
        if b >= b'0' && b <= b'9' {
            value = value.wrapping_mul(10).wrapping_add((b - b'0') as i64);
            *idx += 1;
        } else {
            break;
        }
    }

    if *idx == start {
        *idx = save;
        if start >= src.len() {
            return ScanResult::Eof;
        }
        return ScanResult::Error(*idx);
    }

    if *idx >= src.len() {
        *idx = save;
        return ScanResult::Eof;
    }

    // Reject floats: if next char is '.', 'e', or 'E', this is not an integer.
    let next = src[*idx];
    if next == b'.' || next == b'e' || next == b'E' {
        *idx = save;
        return ScanResult::Error(*idx);
    }

    if negative {
        value = value.wrapping_neg();
    }

    ScanResult::Ok(value)
}

/// Parse a JSON unsigned integer. Streaming-aware: same Eof rule.
pub fn parse_uint64(src: &[u8], idx: &mut usize) -> ScanResult<u64> {
    if *idx >= src.len() {
        return ScanResult::Eof;
    }

    let save = *idx;
    let start = *idx;
    let mut value: u64 = 0;

    while *idx < src.len() {
        let b = src[*idx];
        if b >= b'0' && b <= b'9' {
            value = value.wrapping_mul(10).wrapping_add((b - b'0') as u64);
            *idx += 1;
        } else {
            break;
        }
    }

    if *idx == start {
        if *idx >= src.len() {
            return ScanResult::Eof;
        }
        return ScanResult::Error(*idx);
    }

    if *idx >= src.len() {
        *idx = save;
        return ScanResult::Eof;
    }

    // Reject floats: if next char is '.', 'e', or 'E', this is not an integer.
    let next = src[*idx];
    if next == b'.' || next == b'e' || next == b'E' {
        *idx = save;
        return ScanResult::Error(*idx);
    }

    ScanResult::Ok(value)
}

/// Scan a JSON number without parsing. Returns (start, end).
pub fn scan_number(src: &[u8], idx: &mut usize) -> ScanResult<(usize, usize)> {
    if *idx >= src.len() {
        return ScanResult::Eof;
    }

    let start = *idx;

    // Optional leading minus
    if src[*idx] == b'-' {
        *idx += 1;
    }

    if *idx >= src.len() {
        return ScanResult::Eof;
    }

    // Integer part
    if src[*idx] == b'0' {
        *idx += 1;
    } else if src[*idx] >= b'1' && src[*idx] <= b'9' {
        while *idx < src.len() && src[*idx] >= b'0' && src[*idx] <= b'9' {
            *idx += 1;
        }
    } else {
        return ScanResult::Error(*idx);
    }

    // Fractional part
    if *idx < src.len() && src[*idx] == b'.' {
        *idx += 1;
        let frac_start = *idx;
        while *idx < src.len() && src[*idx] >= b'0' && src[*idx] <= b'9' {
            *idx += 1;
        }
        if *idx == frac_start {
            if *idx >= src.len() {
                return ScanResult::Eof;
            }
            return ScanResult::Error(*idx);
        }
    }

    // Exponent part
    if *idx < src.len() && (src[*idx] == b'e' || src[*idx] == b'E') {
        *idx += 1;
        if *idx < src.len() && (src[*idx] == b'+' || src[*idx] == b'-') {
            *idx += 1;
        }
        let exp_start = *idx;
        while *idx < src.len() && src[*idx] >= b'0' && src[*idx] <= b'9' {
            *idx += 1;
        }
        if *idx == exp_start {
            if *idx >= src.len() {
                return ScanResult::Eof;
            }
            return ScanResult::Error(*idx);
        }
    }

    if *idx == start {
        return ScanResult::Error(start);
    }

    // Streaming guard: if we consumed all available bytes, the number
    // may be truncated (more digits after the current buffer boundary).
    // Return Eof so the caller refills and re-parses.
    if *idx >= src.len() {
        *idx = start;
        return ScanResult::Eof;
    }

    ScanResult::Ok((start, *idx))
}

#[inline]
pub fn parse_true(src: &[u8], idx: &mut usize) -> ScanResult<()> {
    if *idx + 4 > src.len() {
        return ScanResult::Eof;
    }
    let word = u32::from_le_bytes([src[*idx], src[*idx + 1], src[*idx + 2], src[*idx + 3]]);
    if word == u32::from_le_bytes(*b"true") {
        *idx += 4;
        ScanResult::Ok(())
    } else {
        ScanResult::Error(*idx)
    }
}

#[inline]
pub fn parse_false(src: &[u8], idx: &mut usize) -> ScanResult<()> {
    if *idx + 5 > src.len() {
        return ScanResult::Eof;
    }
    let word = u32::from_le_bytes([src[*idx], src[*idx + 1], src[*idx + 2], src[*idx + 3]]);
    if word == u32::from_le_bytes(*b"fals") && src[*idx + 4] == b'e' {
        *idx += 5;
        ScanResult::Ok(())
    } else {
        ScanResult::Error(*idx)
    }
}

#[inline]
pub fn parse_null(src: &[u8], idx: &mut usize) -> ScanResult<()> {
    if *idx + 4 > src.len() {
        return ScanResult::Eof;
    }
    let word = u32::from_le_bytes([src[*idx], src[*idx + 1], src[*idx + 2], src[*idx + 3]]);
    if word == u32::from_le_bytes(*b"null") {
        *idx += 4;
        ScanResult::Ok(())
    } else {
        ScanResult::Error(*idx)
    }
}

/// Skip an arbitrary JSON value (recursive for containers).
pub fn skip_value(src: &[u8], idx: &mut usize) -> ScanResult<()> {
    skip_whitespace(src, idx);
    if *idx >= src.len() {
        return ScanResult::Eof;
    }

    match src[*idx] {
        b'"' => {
            *idx += 1;
            match scan_string(src, idx) {
                ScanResult::Ok(_) => ScanResult::Ok(()),
                ScanResult::Eof => ScanResult::Eof,
                ScanResult::Error(off) => ScanResult::Error(off),
            }
        }
        b'{' => {
            *idx += 1;
            skip_whitespace(src, idx);
            if *idx >= src.len() {
                return ScanResult::Eof;
            }
            if src[*idx] == b'}' {
                *idx += 1;
                return ScanResult::Ok(());
            }
            loop {
                // key
                skip_whitespace(src, idx);
                match expect_byte(src, idx, b'"') {
                    ScanResult::Ok(()) => {}
                    other => {
                        return match other {
                            ScanResult::Eof => ScanResult::Eof,
                            _ => ScanResult::Error(*idx),
                        }
                    }
                }
                match scan_string(src, idx) {
                    ScanResult::Ok(_) => {}
                    ScanResult::Eof => return ScanResult::Eof,
                    ScanResult::Error(off) => return ScanResult::Error(off),
                }
                // colon
                skip_whitespace(src, idx);
                match expect_byte(src, idx, b':') {
                    ScanResult::Ok(()) => {}
                    ScanResult::Eof => return ScanResult::Eof,
                    _ => return ScanResult::Error(*idx),
                }
                // value
                match skip_value(src, idx) {
                    ScanResult::Ok(()) => {}
                    other => return other,
                }
                // comma or end
                skip_whitespace(src, idx);
                if *idx >= src.len() {
                    return ScanResult::Eof;
                }
                match src[*idx] {
                    b'}' => {
                        *idx += 1;
                        return ScanResult::Ok(());
                    }
                    b',' => {
                        *idx += 1;
                    }
                    _ => return ScanResult::Error(*idx),
                }
            }
        }
        b'[' => {
            *idx += 1;
            skip_whitespace(src, idx);
            if *idx >= src.len() {
                return ScanResult::Eof;
            }
            if src[*idx] == b']' {
                *idx += 1;
                return ScanResult::Ok(());
            }
            loop {
                match skip_value(src, idx) {
                    ScanResult::Ok(()) => {}
                    other => return other,
                }
                skip_whitespace(src, idx);
                if *idx >= src.len() {
                    return ScanResult::Eof;
                }
                match src[*idx] {
                    b']' => {
                        *idx += 1;
                        return ScanResult::Ok(());
                    }
                    b',' => {
                        *idx += 1;
                    }
                    _ => return ScanResult::Error(*idx),
                }
            }
        }
        b't' => parse_true(src, idx),
        b'f' => parse_false(src, idx),
        b'n' => parse_null(src, idx),
        b'-' | b'0'..=b'9' => match scan_number(src, idx) {
            ScanResult::Ok(_) => ScanResult::Ok(()),
            ScanResult::Eof => ScanResult::Eof,
            ScanResult::Error(off) => ScanResult::Error(off),
        },
        _ => ScanResult::Error(*idx),
    }
}

/// Unescape a JSON string body into `dst`. Returns bytes written.
/// `raw` is src[start..end] from a prior scan_string call.
/// Caller ensures `dst.len() >= raw.len()`.
pub fn unescape_to(raw: &[u8], dst: &mut [u8]) -> usize {
    let mut ri = 0;
    let mut wi = 0;
    while ri < raw.len() {
        if raw[ri] == b'\\' && ri + 1 < raw.len() {
            ri += 1;
            match raw[ri] {
                b'"' => {
                    dst[wi] = b'"';
                    wi += 1;
                }
                b'\\' => {
                    dst[wi] = b'\\';
                    wi += 1;
                }
                b'/' => {
                    dst[wi] = b'/';
                    wi += 1;
                }
                b'b' => {
                    dst[wi] = 0x08;
                    wi += 1;
                }
                b'f' => {
                    dst[wi] = 0x0C;
                    wi += 1;
                }
                b'n' => {
                    dst[wi] = b'\n';
                    wi += 1;
                }
                b'r' => {
                    dst[wi] = b'\r';
                    wi += 1;
                }
                b't' => {
                    dst[wi] = b'\t';
                    wi += 1;
                }
                b'u' => {
                    if ri + 4 < raw.len() {
                        let cp = decode_hex4(raw[ri + 1], raw[ri + 2], raw[ri + 3], raw[ri + 4]);
                        ri += 4;
                        // Encode as UTF-8
                        if cp < 0x80 {
                            dst[wi] = cp as u8;
                            wi += 1;
                        } else if cp < 0x800 {
                            dst[wi] = 0xC0 | ((cp >> 6) as u8);
                            dst[wi + 1] = 0x80 | ((cp & 0x3F) as u8);
                            wi += 2;
                        } else {
                            dst[wi] = 0xE0 | ((cp >> 12) as u8);
                            dst[wi + 1] = 0x80 | (((cp >> 6) & 0x3F) as u8);
                            dst[wi + 2] = 0x80 | ((cp & 0x3F) as u8);
                            wi += 3;
                        }
                    }
                }
                other => {
                    dst[wi] = other;
                    wi += 1;
                }
            }
            ri += 1;
        } else {
            dst[wi] = raw[ri];
            wi += 1;
            ri += 1;
        }
    }
    wi
}

#[inline]
fn decode_hex4(a: u8, b: u8, c: u8, d: u8) -> u32 {
    (hex_val(a) << 12) | (hex_val(b) << 8) | (hex_val(c) << 4) | hex_val(d)
}

#[inline]
fn hex_val(c: u8) -> u32 {
    match c {
        b'0'..=b'9' => (c - b'0') as u32,
        b'a'..=b'f' => (c - b'a' + 10) as u32,
        b'A'..=b'F' => (c - b'A' + 10) as u32,
        _ => 0,
    }
}
