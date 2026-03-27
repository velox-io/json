//! Three-tier float64 parsing for rsdec (no_std).
//!
//! Tier 1: Exact pow10 (<=15 digits, |power10| <= 22)
//! Tier 2: Eisel-Lemire algorithm (<=19 digits)
//! Tier 3: uscale/fpparse fallback

include!("float_tables.rs");

/// Exact powers of 10 as f64 (10^0 through 10^22).
static POW10_F64: [f64; 23] = [
    1e0, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9, 1e10, 1e11, 1e12, 1e13, 1e14, 1e15,
    1e16, 1e17, 1e18, 1e19, 1e20, 1e21, 1e22,
];

#[inline]
fn fp_log2_pow10(x: i32) -> i32 {
    (x as i64 * 108853 >> 15) as i32
}

#[inline]
fn fp_bits_len64(x: u64) -> i32 {
    if x == 0 { 0 } else { 64 - x.leading_zeros() as i32 }
}

#[inline]
fn fp_min_int(a: i32, b: i32) -> i32 {
    if a < b { a } else { b }
}

/// 128-bit multiply: returns (hi, lo).
#[inline]
fn mul64(a: u64, b: u64) -> (u64, u64) {
    let r = (a as u128) * (b as u128);
    ((r >> 64) as u64, r as u64)
}

/// Extract (mantissa, power10, digit_count, negative) from JSON number bytes.
pub fn scan_float_parts(src: &[u8]) -> (u64, i32, i32, bool) {
    let len = src.len();
    let mut i = 0;

    let negative = i < len && src[i] == b'-';
    if negative {
        i += 1;
    }

    let mut mantissa: u64 = 0;
    let mut digit_count: i32 = 0;
    let mut frac_start: i32 = -1;

    // Integer digits
    if i < len && src[i] == b'0' {
        i += 1;
    } else {
        while i < len && src[i] >= b'0' && src[i] <= b'9' {
            if digit_count < 19 {
                mantissa = mantissa * 10 + (src[i] - b'0') as u64;
            }
            digit_count += 1;
            i += 1;
        }
    }

    // Fractional digits
    if i < len && src[i] == b'.' {
        i += 1;
        frac_start = digit_count;
        if digit_count == 0 {
            while i < len && src[i] == b'0' {
                digit_count += 1;
                i += 1;
            }
        }
        while i < len && src[i] >= b'0' && src[i] <= b'9' {
            if digit_count < 19 {
                mantissa = mantissa * 10 + (src[i] - b'0') as u64;
            }
            digit_count += 1;
            i += 1;
        }
    }

    // Exponent
    let mut exponent: i32 = 0;
    if i < len && (src[i] == b'e' || src[i] == b'E') {
        i += 1;
        let mut exp_neg = false;
        if i < len && (src[i] == b'+' || src[i] == b'-') {
            exp_neg = src[i] == b'-';
            i += 1;
        }
        while i < len && src[i] >= b'0' && src[i] <= b'9' {
            exponent = exponent * 10 + (src[i] - b'0') as i32;
            if exponent > 400 {
                i += 1;
                while i < len && src[i] >= b'0' && src[i] <= b'9' {
                    i += 1;
                }
                break;
            }
            i += 1;
        }
        if exp_neg {
            exponent = -exponent;
        }
    }

    let mut power10 = exponent;
    if frac_start >= 0 {
        power10 -= digit_count - frac_start;
    }

    (mantissa, power10, digit_count, negative)
}

/// Tier 1: Exact pow10. Returns Some(f64) if <=15 digits and |power10| <= 22.
#[inline]
fn tier1_exact_pow10(mantissa: u64, power10: i32, digit_count: i32, negative: bool) -> Option<f64> {
    if digit_count > 15 || power10 < -22 || power10 > 22 {
        return None;
    }
    let mut f = mantissa as f64;
    if power10 >= 0 {
        f *= POW10_F64[power10 as usize];
    } else {
        f /= POW10_F64[(-power10) as usize];
    }
    if negative {
        f = -f;
    }
    Some(f)
}

/// Tier 2: Eisel-Lemire algorithm. Returns Some(bits) on success.
#[inline]
fn eisel_lemire(mantissa: u64, power10: i32) -> Option<u64> {
    if power10 < EL_POW10_MIN || power10 > EL_POW10_MAX {
        return None;
    }

    let power = &EL_POW10_TAB[(power10 - EL_POW10_MIN) as usize];

    // Binary exponent estimate
    let binary_exp: i32 = 1 + ((power10 as i64 * 108853 >> 15) as i32);

    // Normalize mantissa
    let leading_zeros = mantissa.leading_zeros();
    let mantissa = mantissa << leading_zeros;
    let mut result_exp = (binary_exp as i64 + 63 - (-1023)) as u64 - leading_zeros as u64;

    // 128-bit multiply
    let (mut prod_hi, prod_lo) = mul64(mantissa, power[0]);

    // Refinement check
    let mut final_lo = prod_lo;
    if (prod_hi & 0x1FF) == 0x1FF && prod_lo.wrapping_add(mantissa) < mantissa {
        let (cross_hi, cross_lo) = mul64(mantissa, power[1]);
        let refined_lo = prod_lo.wrapping_add(cross_hi);
        if refined_lo < prod_lo {
            prod_hi += 1;
        }
        if (prod_hi & 0x1FF) == 0x1FF
            && refined_lo.wrapping_add(1) == 0
            && cross_lo.wrapping_add(mantissa) < mantissa
        {
            return None;
        }
        final_lo = refined_lo;
    }

    // Extract 54-bit mantissa
    let top_bit = prod_hi >> 63;
    let mut result_mantissa = prod_hi >> (top_bit + 9);
    result_exp -= 1 ^ top_bit;

    // Ambiguous halfway
    if final_lo == 0 && (prod_hi & 0x1FF) == 0 && (result_mantissa & 3) == 1 {
        return None;
    }

    // Round to nearest even
    result_mantissa += result_mantissa & 1;
    result_mantissa >>= 1;
    if result_mantissa >> 53 > 0 {
        result_mantissa >>= 1;
        result_exp += 1;
    }

    // Overflow or underflow
    if result_exp.wrapping_sub(1) >= 0x7FF - 1 {
        return None;
    }

    Some(result_exp << 52 | (result_mantissa & ((1u64 << 52) - 1)))
}

/// Tier 3: uscale (fpparse) fallback.
fn fpparse_uscale(d: u64, p: i32) -> f64 {
    if d == 0 {
        return 0.0;
    }

    if p < US_POW10_MIN {
        return 0.0;
    }
    if p > US_POW10_MAX {
        return f64::from_bits(0x7FF0000000000000u64); // +Inf
    }

    let b = fp_bits_len64(d);
    let lp = fp_log2_pow10(p);
    let e = fp_min_int(1074, 53 - b - lp);

    let s = -((e - (64 - b)) + lp + 3);
    let pm_hi = US_POW10_TAB[(p - US_POW10_MIN) as usize][0];
    let pm_lo = US_POW10_TAB[(p - US_POW10_MIN) as usize][1];

    let x = d << (64 - b) as u32;

    let (mut hi, mid) = mul64(x, pm_hi);
    let mut sticky: u64 = 1;
    let s_mask = (1u64 << (s as u32 & 63)) - 1;
    if (hi & s_mask) == 0 {
        let (mid2, _dummy) = mul64(x, pm_lo);
        sticky = if mid.wrapping_sub(mid2) > 1 { 1 } else { 0 };
        if mid < mid2 {
            hi = hi.wrapping_sub(1);
        }
    }
    let mut u = (hi >> s as u32) | sticky;

    // Branch-free normalization
    let unmin_threshold: u64 = (1u64 << 55) - 2;
    let shift = if u >= unmin_threshold { 1u32 } else { 0u32 };
    u = (u >> shift) | (u & 1);
    let e = e - shift as i32;

    // round()
    let m = (u + 1 + ((u >> 2) & 1)) >> 2;

    // pack64
    if (m & (1u64 << 52)) == 0 {
        return f64::from_bits(m);
    }
    f64::from_bits((m & !(1u64 << 52)) | ((1075u64 + (-e) as u64) << 52))
}

/// Top-level three-tier float64 dispatcher.
pub fn parse_float64(src: &[u8]) -> f64 {
    let (mantissa, power10, digit_count, negative) = scan_float_parts(src);

    // Tier 1
    if let Some(result) = tier1_exact_pow10(mantissa, power10, digit_count, negative) {
        return result;
    }

    // Zero
    if mantissa == 0 {
        if negative {
            return f64::from_bits(1u64 << 63);
        }
        return 0.0;
    }

    // Tier 2: Eisel-Lemire
    if digit_count <= 19 {
        if let Some(mut bits) = eisel_lemire(mantissa, power10) {
            if negative {
                bits |= 1u64 << 63;
            }
            return f64::from_bits(bits);
        }
    }

    // Tier 3: uscale
    let p3_power10 = if digit_count > 19 {
        power10 + (digit_count - 19)
    } else {
        power10
    };
    let f = fpparse_uscale(mantissa, p3_power10);
    if negative { -f } else { f }
}
