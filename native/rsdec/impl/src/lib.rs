//! vjson-rsdec: no_std Rust JSON decoder for velox-io/json.
//!
//! This crate compiles to a C-ABI static library (.a), which is then
//! prelinked into a .syso for Go's internal linker.

#![no_std]

pub mod debug;
mod float;
mod insn;
pub mod parser;
pub mod resume;
mod scanner;
mod typedesc;
mod writer;

#[panic_handler]
fn panic(_info: &core::panic::PanicInfo) -> ! {
    loop {}
}

// ============================================================
//  DecExecCtx — 128 bytes, matches Go layout exactly
// ============================================================

/// Exit codes for DecExecCtx.exit_code.
#[allow(dead_code)]
pub mod exit {
    pub const OK: i32 = 0;
    pub const UNEXPECTED_EOF: i32 = 1;
    pub const SYNTAX_ERROR: i32 = 2;
    pub const TYPE_ERROR: i32 = 3;

    pub const YIELD_STRING: i32 = 10;
    pub const YIELD_ALLOC_SLICE: i32 = 11;
    pub const YIELD_GROW_SLICE: i32 = 12;
    pub const YIELD_ARENA_FULL: i32 = 13;
    pub const YIELD_MAP_INIT: i32 = 14;
    pub const YIELD_MAP_ASSIGN: i32 = 15;
    pub const YIELD_INSN_FLUSH: i32 = 16;
    pub const YIELD_ALLOC_POINTER: i32 = 17;
}

/// DecExecCtx — shared Rust/Go context (128 bytes, repr(C)).
#[repr(C)]
pub struct DecExecCtx {
    pub src_ptr: *const u8,
    pub src_len: u32,
    pub idx: u32,
    pub cur_base: *mut u8,
    pub ti_ptr: *const u8,
    pub exit_code: i32,
    pub flags: u32,
    pub scratch_ptr: *mut u8,
    pub scratch_cap: u32,
    pub scratch_len: u32,
    pub _pad_56: u32,
    pub err_detail: u32,

    pub yield_param0: u64,
    pub yield_param1: u64,
    pub yield_param2: u64,
    pub resume_ptr: *mut u8,
    pub resume_cap: u16,
    pub resume_depth: u16,
    pub _pad1: u32,
    pub insn_ptr: *mut u8,
    pub insn_len: u32,
    pub insn_cap: u32,
    pub _reserved: [u8; 8],
}

const _: () = {
    let _ = [0u8; 128 - core::mem::size_of::<DecExecCtx>()];
    let _ = [0u8; core::mem::size_of::<DecExecCtx>() - 128];
};

// ============================================================
//  Entry points
// ============================================================

#[no_mangle]
pub unsafe extern "C" fn vj_dec_exec(ctx: *mut DecExecCtx) {
    let c = &mut *ctx;
    c.resume_depth = 0;
    parser::rd_exec(c);
}

#[no_mangle]
pub unsafe extern "C" fn vj_dec_resume(ctx: *mut DecExecCtx) {
    let c = &mut *ctx;
    resume::rd_resume(c);
}
