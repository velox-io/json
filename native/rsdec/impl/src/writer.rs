//! Primitive value writers: unaligned writes to raw Go memory.

use crate::typedesc::kind;

#[inline]
pub unsafe fn write_int(ptr: *mut u8, k: u8, val: i64) {
    match k {
        kind::INT8 => core::ptr::write_unaligned(ptr as *mut i8, val as i8),
        kind::INT16 => core::ptr::write_unaligned(ptr as *mut i16, val as i16),
        kind::INT32 => core::ptr::write_unaligned(ptr as *mut i32, val as i32),
        kind::INT64 => core::ptr::write_unaligned(ptr as *mut i64, val),
        kind::INT => {
            core::ptr::write_unaligned(ptr as *mut i64, val);
        }
        _ => {}
    }
}

#[inline]
pub unsafe fn write_uint(ptr: *mut u8, k: u8, val: u64) {
    match k {
        kind::UINT8 => core::ptr::write_unaligned(ptr as *mut u8, val as u8),
        kind::UINT16 => core::ptr::write_unaligned(ptr as *mut u16, val as u16),
        kind::UINT32 => core::ptr::write_unaligned(ptr as *mut u32, val as u32),
        kind::UINT64 => core::ptr::write_unaligned(ptr as *mut u64, val),
        kind::UINT => {
            core::ptr::write_unaligned(ptr as *mut u64, val);
        }
        _ => {}
    }
}

#[inline]
pub unsafe fn write_bool(ptr: *mut u8, val: bool) {
    *ptr = val as u8;
}

#[inline]
pub unsafe fn write_float32(ptr: *mut u8, val: f32) {
    core::ptr::write_unaligned(ptr as *mut f32, val);
}

#[inline]
pub unsafe fn write_float64(ptr: *mut u8, val: f64) {
    core::ptr::write_unaligned(ptr as *mut f64, val);
}

#[inline]
#[allow(dead_code)]
pub unsafe fn write_zero(ptr: *mut u8, n: usize) {
    core::ptr::write_bytes(ptr, 0, n);
}
