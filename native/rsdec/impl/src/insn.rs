//! Instruction buffer for deferred Go operations.
//!
//! Rust appends typed instructions (tag + payload); Go executes them
//! in order during YIELD_INSN_FLUSH.

use crate::exit;
use crate::DecExecCtx;

/// Flush threshold. Must be >= largest single instruction (13B).
pub const INSN_RESERVE: usize = 24;

// ============================================================
//  Tag constants
// ============================================================

pub mod tag {
    pub const NOP: u8 = 0x00;

    // Context setting
    pub const SET_TARGET: u8 = 0x01;
    pub const SET_KEY: u8 = 0x02;

    // Container instructions (0x10-0x1F)
    pub const MAKE_OBJECT: u8 = 0x10;
    pub const MAKE_ARRAY: u8 = 0x11;
    pub const CLOSE_OBJECT: u8 = 0x12;
    pub const CLOSE_ARRAY: u8 = 0x13;

    // Value emission (0x20-0x3F)
    pub const EMIT_NULL: u8 = 0x20;
    pub const EMIT_TRUE: u8 = 0x21;
    pub const EMIT_FALSE: u8 = 0x22;
    pub const EMIT_INT: u8 = 0x23;
    pub const EMIT_FLOAT: u8 = 0x25;
    pub const EMIT_STRING: u8 = 0x26;
    pub const EMIT_STRING_ESC: u8 = 0x27;
    pub const EMIT_NUMBER: u8 = 0x28;

    // Deferred Go calls (0x60-0x7F) — reserved for future use
    // pub const CALL_TIME_PARSE: u8 = 0x60;
    // pub const CALL_UNMARSHALER: u8 = 0x61;
}

// ============================================================
//  Core
// ============================================================

/// True if remaining instruction buffer space < INSN_RESERVE.
#[inline(always)]
pub unsafe fn insn_near_full(ctx: &DecExecCtx) -> bool {
    if ctx.insn_ptr.is_null() {
        return true;
    }
    let remaining = ctx.insn_cap as usize - ctx.insn_len as usize;
    remaining < INSN_RESERVE
}

/// Append raw bytes. Caller must ensure space.
#[inline(always)]
pub unsafe fn insn_write(ctx: &mut DecExecCtx, bytes: &[u8]) {
    let dst = ctx.insn_ptr.add(ctx.insn_len as usize);
    core::ptr::copy_nonoverlapping(bytes.as_ptr(), dst, bytes.len());
    ctx.insn_len += bytes.len() as u32;
}

/// Append bytes and flush if needed. Returns true if flush was yielded.
#[inline]
pub unsafe fn insn_append(ctx: &mut DecExecCtx, bytes: &[u8]) -> bool {
    insn_write(ctx, bytes);
    insn_flush_if_needed(ctx)
}

/// Yield INSN_FLUSH if remaining < INSN_RESERVE. Returns true if yielded.
#[inline]
pub unsafe fn insn_flush_if_needed(ctx: &mut DecExecCtx) -> bool {
    let remaining = ctx.insn_cap as usize - ctx.insn_len as usize;
    if remaining < INSN_RESERVE {
        ctx.exit_code = exit::YIELD_INSN_FLUSH;
        true
    } else {
        false
    }
}

// ============================================================
//  Typed emitters
// ============================================================

/// SET_TARGET [tag:1][target:8] = 9B
#[inline]
pub unsafe fn insn_emit_set_target(ctx: &mut DecExecCtx, target: u64) {
    let mut buf = [0u8; 9];
    buf[0] = tag::SET_TARGET;
    buf[1..9].copy_from_slice(&target.to_le_bytes());
    insn_write(ctx, &buf);
}

/// SET_KEY [tag:1][ptr:8][len:4] = 13B
#[inline]
pub unsafe fn insn_emit_set_key(ctx: &mut DecExecCtx, ptr: *const u8, len: u32) {
    let mut buf = [0u8; 13];
    buf[0] = tag::SET_KEY;
    buf[1..9].copy_from_slice(&(ptr as u64).to_le_bytes());
    buf[9..13].copy_from_slice(&len.to_le_bytes());
    insn_write(ctx, &buf);
}

/// MAKE_OBJECT [tag:1][hint:4] = 5B
#[inline]
pub unsafe fn insn_emit_make_object(ctx: &mut DecExecCtx, hint: u32) {
    let mut buf = [0u8; 5];
    buf[0] = tag::MAKE_OBJECT;
    buf[1..5].copy_from_slice(&hint.to_le_bytes());
    insn_write(ctx, &buf);
}

/// MAKE_ARRAY [tag:1][hint:4] = 5B
#[inline]
pub unsafe fn insn_emit_make_array(ctx: &mut DecExecCtx, hint: u32) {
    let mut buf = [0u8; 5];
    buf[0] = tag::MAKE_ARRAY;
    buf[1..5].copy_from_slice(&hint.to_le_bytes());
    insn_write(ctx, &buf);
}

/// CLOSE_OBJECT [tag:1]
#[inline]
pub unsafe fn insn_emit_close_object(ctx: &mut DecExecCtx) {
    insn_write(ctx, &[tag::CLOSE_OBJECT]);
}

/// CLOSE_ARRAY [tag:1]
#[inline]
pub unsafe fn insn_emit_close_array(ctx: &mut DecExecCtx) {
    insn_write(ctx, &[tag::CLOSE_ARRAY]);
}

// ---- Leaf values ----

/// EMIT_NULL [tag:1]
#[inline]
pub unsafe fn insn_emit_null(ctx: &mut DecExecCtx) {
    insn_write(ctx, &[tag::EMIT_NULL]);
}

/// EMIT_TRUE [tag:1]
#[inline]
pub unsafe fn insn_emit_true(ctx: &mut DecExecCtx) {
    insn_write(ctx, &[tag::EMIT_TRUE]);
}

/// EMIT_FALSE [tag:1]
#[inline]
pub unsafe fn insn_emit_false(ctx: &mut DecExecCtx) {
    insn_write(ctx, &[tag::EMIT_FALSE]);
}

/// EMIT_INT [tag:1][value:8] = 9B
#[inline]
pub unsafe fn insn_emit_int(ctx: &mut DecExecCtx, value: i64) {
    let mut buf = [0u8; 9];
    buf[0] = tag::EMIT_INT;
    buf[1..9].copy_from_slice(&value.to_le_bytes());
    insn_write(ctx, &buf);
}

/// EMIT_FLOAT [tag:1][src_ptr:8][src_len:4] = 13B
#[inline]
pub unsafe fn insn_emit_float(ctx: &mut DecExecCtx, src_ptr: *const u8, src_len: u32) {
    let mut buf = [0u8; 13];
    buf[0] = tag::EMIT_FLOAT;
    buf[1..9].copy_from_slice(&(src_ptr as u64).to_le_bytes());
    buf[9..13].copy_from_slice(&src_len.to_le_bytes());
    insn_write(ctx, &buf);
}

/// EMIT_STRING (zero-copy) [tag:1][ptr:8][len:4] = 13B
#[inline]
pub unsafe fn insn_emit_string(ctx: &mut DecExecCtx, ptr: *const u8, len: u32) {
    let mut buf = [0u8; 13];
    buf[0] = tag::EMIT_STRING;
    buf[1..9].copy_from_slice(&(ptr as u64).to_le_bytes());
    buf[9..13].copy_from_slice(&len.to_le_bytes());
    insn_write(ctx, &buf);
}

/// EMIT_STRING_ESC (arena) [tag:1][arena_off:4][len:4] = 9B
#[inline]
pub unsafe fn insn_emit_string_esc(ctx: &mut DecExecCtx, arena_off: u32, len: u32) {
    let mut buf = [0u8; 9];
    buf[0] = tag::EMIT_STRING_ESC;
    buf[1..5].copy_from_slice(&arena_off.to_le_bytes());
    buf[5..9].copy_from_slice(&len.to_le_bytes());
    insn_write(ctx, &buf);
}

/// EMIT_NUMBER (raw bytes) [tag:1][ptr:8][len:4] = 13B
#[inline]
pub unsafe fn insn_emit_number(ctx: &mut DecExecCtx, ptr: *const u8, len: u32) {
    let mut buf = [0u8; 13];
    buf[0] = tag::EMIT_NUMBER;
    buf[1..9].copy_from_slice(&(ptr as u64).to_le_bytes());
    buf[9..13].copy_from_slice(&len.to_le_bytes());
    insn_write(ctx, &buf);
}
