//! Split-entry recursive descent parser.
//!
//! Call graph:
//!
//!   rd_exec → rd_parse_value        (dispatch on src[idx] first byte)
//!     ├─ rd_parse_to_struct → rd_body_to_struct   (JSON object → Go struct)
//!     ├─ rd_parse_to_slice  → rd_body_to_slice    (JSON array  → Go slice)
//!     ├─ rd_parse_to_array  → rd_body_to_array    (JSON array  → Go [N]T)
//!     ├─ rd_parse_to_map    → rd_body_to_map      (JSON object → Go map)
//!     └─ rd_parse_to_any    → any_object/any_array (insn-mode for interface{})
//!
//! resume.rs pops the innermost frame and dispatches back to the
//! corresponding body function. parser.rs only calls resume::push_frame.
//!
//! Body functions use the reserve-slot pattern: reserve a slot on the
//! resume stack at entry, write a frame on Suspend, release on Ok/Error.

use crate::exit;
use crate::resume::{self, FrameKind, ResumeFrame};
use crate::scanner::{self, ScanResult};
use crate::float;
use crate::insn;
use crate::typedesc::{kind, DecArrayDesc, DecMapDesc, DecPointerDesc, DecSliceDesc, DecStructDesc};
use crate::writer;
use crate::DecExecCtx;

pub enum Rd {
    Ok,
    Suspend, // resumable: exit_code is EOF or yield
    Error,   // terminal: exit_code is SYNTAX_ERROR or TYPE_ERROR
}

#[inline(always)]
unsafe fn set_error(ctx: &mut DecExecCtx, idx: usize, code: i32) {
    ctx.idx = idx as u32;
    ctx.err_detail = idx as u32;
    ctx.exit_code = code;
}

#[inline(always)]
pub unsafe fn set_eof(ctx: &mut DecExecCtx, idx: usize) {
    ctx.idx = idx as u32;
    ctx.exit_code = exit::UNEXPECTED_EOF;
}

// ============================================================
//  Top-level entry
// ============================================================

pub unsafe fn rd_exec(ctx: &mut DecExecCtx) {
    let src = core::slice::from_raw_parts(ctx.src_ptr, ctx.src_len as usize);
    let kind = *(ctx.ti_ptr as *const u8);

    match rd_parse_value(ctx, src, kind, ctx.ti_ptr, ctx.cur_base) {
        Rd::Ok => ctx.exit_code = exit::OK,
        Rd::Suspend => {
            // No child frame pushed (e.g. EOF before any container) —
            // push ROOT so resume can retry.
            if ctx.resume_depth == 0 {
                resume::push_frame(
                    ctx,
                    ResumeFrame {
                        base: ctx.cur_base as *mut u8,
                        ti_ptr: ctx.ti_ptr,
                        kind: FrameKind::Root,
                        state: 0,
                    },
                );
            }
        }
        Rd::Error => { /* exit_code already set by set_error */ }
    }
}

// ============================================================
//  Value dispatch (src-byte driven)
// ============================================================

/// Dispatch on src[idx] leading byte, parse the JSON value, write to
/// `target` according to the destination `kind`.
pub unsafe fn rd_parse_value(
    ctx: &mut DecExecCtx,
    src: &[u8],
    k: u8,
    desc: *const u8,
    target: *mut u8,
) -> Rd {
    let idx = &mut (ctx.idx as usize);
    scanner::skip_whitespace(src, idx);

    if *idx >= src.len() {
        set_eof(ctx, *idx);
        return Rd::Suspend;
    }

    if k == kind::ANY {
        // Sync ctx.idx: skip_whitespace advanced the local idx but not ctx.idx.
        // rd_parse_to_any reads ctx.idx afresh, so it must be current.
        ctx.idx = *idx as u32;
        // Emit SET_TARGET so the Go instruction executor knows where to write.
        // This is only needed for "root" entry into insn mode from a typed
        // context (e.g. struct field of type any). Nested any values inside
        // insn-mode containers don't need SET_TARGET — they are implicitly
        // associated with the current container via SET_KEY / array append.
        if !target.is_null() {
            insn::insn_emit_set_target(ctx, target as u64);
        }
        return rd_parse_to_any(ctx, src);
    }

    if k == kind::POINTER {
        return rd_parse_to_pointer(ctx, src, idx, target, desc as *const DecPointerDesc);
    }

    match src[*idx] {
        b'{' => match *(desc as *const u8) {
            kind::STRUCT => rd_parse_to_struct(ctx, src, target, desc as *const DecStructDesc),
            kind::MAP => rd_parse_to_map(ctx, src, target, desc as *const DecMapDesc),
            _ => {
                set_error(ctx, *idx, exit::TYPE_ERROR);
                Rd::Error
            }
        },

        b'[' => match k {
            kind::SLICE => rd_parse_to_slice(ctx, src, target, desc as *const DecSliceDesc),
            kind::ARRAY => rd_parse_to_array(ctx, src, target, desc as *const DecArrayDesc),
            _ => {
                ctx.idx = *idx as u32;
                rd_parse_to_skip(ctx, src, idx)
            }
        },

        b'"' => {
            *idx += 1;
            match scanner::scan_string(src, idx) {
                ScanResult::Ok((start, end, has_escape)) => {
                    ctx.idx = *idx as u32;
                    if k != kind::STRING {
                        return Rd::Ok;
                    }
                    if !has_escape {
                        let ptr = src.as_ptr().add(start);
                        core::ptr::write_unaligned(target as *mut usize, ptr as usize);
                        core::ptr::write_unaligned((target as *mut usize).add(1), end - start);
                    } else {
                        let raw_len = end - start;
                        let arena_used = ctx.scratch_len as usize;
                        let arena_cap = ctx.scratch_cap as usize;

                        if arena_used + raw_len > arena_cap {
                            // Arena full — don't advance idx so the caller
                            // rolls back to field_start for re-parse.
                            ctx.exit_code = exit::YIELD_ARENA_FULL;
                            ctx.yield_param0 = raw_len as u64;
                            return Rd::Suspend;
                        }

                        let arena = core::slice::from_raw_parts_mut(
                            ctx.scratch_ptr.add(arena_used),
                            arena_cap - arena_used,
                        );
                        let raw = &src[start..end];
                        let written = scanner::unescape_to(raw, arena);

                        let str_ptr = ctx.scratch_ptr.add(arena_used);
                        core::ptr::write_unaligned(target as *mut usize, str_ptr as usize);
                        core::ptr::write_unaligned((target as *mut usize).add(1), written);

                        ctx.scratch_len = (arena_used + written) as u32;
                    }
                    Rd::Ok
                }
                ScanResult::Eof => {
                    set_eof(ctx, *idx);
                    Rd::Suspend
                }
                ScanResult::Error(o) => {
                    set_error(ctx, o, exit::SYNTAX_ERROR);
                    Rd::Error
                }
            }
        }

        b'-' | b'0'..=b'9' => {
            if kind::is_signed_int(k) {
                match scanner::parse_int64(src, idx) {
                    ScanResult::Ok(v) => {
                        writer::write_int(target, k, v);
                        ctx.idx = *idx as u32;
                        Rd::Ok
                    }
                    ScanResult::Eof => {
                        set_eof(ctx, *idx);
                        Rd::Suspend
                    }
                    ScanResult::Error(o) => {
                        set_error(ctx, o, exit::SYNTAX_ERROR);
                        Rd::Error
                    }
                }
            } else if kind::is_unsigned_int(k) {
                match scanner::parse_uint64(src, idx) {
                    ScanResult::Ok(v) => {
                        writer::write_uint(target, k, v);
                        ctx.idx = *idx as u32;
                        Rd::Ok
                    }
                    ScanResult::Eof => {
                        set_eof(ctx, *idx);
                        Rd::Suspend
                    }
                    ScanResult::Error(o) => {
                        set_error(ctx, o, exit::SYNTAX_ERROR);
                        Rd::Error
                    }
                }
            } else if k == kind::FLOAT32 || k == kind::FLOAT64 {
                let start = *idx;
                match scanner::scan_number(src, idx) {
                    ScanResult::Ok(_) => {
                        let f = float::parse_float64(&src[start..*idx]);
                        if k == kind::FLOAT32 {
                            writer::write_float32(target, f as f32);
                        } else {
                            writer::write_float64(target, f);
                        }
                        ctx.idx = *idx as u32;
                        Rd::Ok
                    }
                    ScanResult::Eof => {
                        set_eof(ctx, *idx);
                        Rd::Suspend
                    }
                    ScanResult::Error(o) => {
                        set_error(ctx, o, exit::SYNTAX_ERROR);
                        Rd::Error
                    }
                }
            } else {
                match scanner::scan_number(src, idx) {
                    ScanResult::Ok(_) => {
                        ctx.idx = *idx as u32;
                        Rd::Ok
                    }
                    ScanResult::Eof => {
                        set_eof(ctx, *idx);
                        Rd::Suspend
                    }
                    ScanResult::Error(o) => {
                        set_error(ctx, o, exit::SYNTAX_ERROR);
                        Rd::Error
                    }
                }
            }
        }

        b't' => match scanner::parse_true(src, idx) {
            ScanResult::Ok(()) => {
                ctx.idx = *idx as u32;
                if k == kind::BOOL {
                    writer::write_bool(target, true);
                }
                Rd::Ok
            }
            ScanResult::Eof => {
                set_eof(ctx, *idx);
                Rd::Suspend
            }
            ScanResult::Error(o) => {
                set_error(ctx, o, exit::SYNTAX_ERROR);
                Rd::Error
            }
        },

        b'f' => match scanner::parse_false(src, idx) {
            ScanResult::Ok(()) => {
                ctx.idx = *idx as u32;
                if k == kind::BOOL {
                    writer::write_bool(target, false);
                }
                Rd::Ok
            }
            ScanResult::Eof => {
                set_eof(ctx, *idx);
                Rd::Suspend
            }
            ScanResult::Error(o) => {
                set_error(ctx, o, exit::SYNTAX_ERROR);
                Rd::Error
            }
        },

        b'n' => match scanner::parse_null(src, idx) {
            ScanResult::Ok(()) => {
                ctx.idx = *idx as u32;
                match k {
                    kind::BOOL => writer::write_bool(target, false),
                    k if kind::is_signed_int(k) => writer::write_int(target, k, 0),
                    k if kind::is_unsigned_int(k) => writer::write_uint(target, k, 0),
                    kind::STRING => writer::write_zero(target, 16),
                    _ => {}
                }
                Rd::Ok
            }
            ScanResult::Eof => {
                set_eof(ctx, *idx);
                Rd::Suspend
            }
            ScanResult::Error(o) => {
                set_error(ctx, o, exit::SYNTAX_ERROR);
                Rd::Error
            }
        },

        _ => {
            set_error(ctx, *idx, exit::SYNTAX_ERROR);
            Rd::Error
        }
    }
}

// ============================================================
//  Pointer (*T)
// ============================================================

/// Parse a JSON value into a Go pointer field.
///
/// Option C design:
/// - JSON null  → set *target = nil, return Ok
/// - *target != nil → reuse existing allocation, parse element into it
/// - *target == nil → yield to Go for allocation, then resume to parse
#[inline(never)]
unsafe fn rd_parse_to_pointer(
    ctx: &mut DecExecCtx,
    src: &[u8],
    idx: &mut usize,
    target: *mut u8,
    desc: *const DecPointerDesc,
) -> Rd {
    let desc_ref = &*desc;

    // Check for JSON null → set pointer to nil.
    if src[*idx] == b'n' {
        if *idx + 4 > src.len() {
            set_eof(ctx, *idx);
            return Rd::Suspend;
        }
        let word = core::ptr::read_unaligned(src.as_ptr().add(*idx) as *const u32);
        if word != 0x6c6c756e {
            // not "null"
            set_error(ctx, *idx, exit::SYNTAX_ERROR);
            return Rd::Error;
        }
        *idx += 4;
        ctx.idx = *idx as u32;
        // Set the pointer slot to nil (zero the pointer-sized value).
        core::ptr::write(target as *mut usize, 0);
        return Rd::Ok;
    }

    // Read the current pointer value from the pointer slot.
    let elem_ptr = *(target as *const *mut u8);

    if !elem_ptr.is_null() {
        // Non-nil pointer: reuse existing allocation.
        // Parse the element value directly into the existing memory.
        return rd_parse_value(ctx, src, desc_ref.elem_kind, desc_ref.elem_desc, elem_ptr);
    }

    // Nil pointer: yield to Go for allocation.
    // Go will allocate, write the pointer to *target, then resume.
    //
    // We do NOT push a Pointer frame here. Instead, we return Suspend
    // and let the parent (struct body/slice body) handle rollback.
    // After Go allocates and resumes, the parent re-parses the field.
    // On re-entry, the pointer is now non-nil, so the reuse path is taken.
    ctx.idx = *idx as u32;
    ctx.yield_param0 = desc_ref.elem_rtype as u64;  // element rtype for allocation
    ctx.yield_param1 = target as u64;                // pointer slot address
    ctx.yield_param2 = desc_ref.elem_has_ptr as u64; // whether elem contains pointers
    ctx.exit_code = exit::YIELD_ALLOC_POINTER;
    Rd::Suspend
}

// ============================================================
//  Skip (streaming skip of unknown/mismatched values)
// ============================================================

/// Skip a JSON value, streaming across buffer boundaries.
///
/// Called when:
/// - struct field name is unknown → skip value
/// - type mismatch (e.g. JSON array but target is int) → skip value
/// - array overflow (more elements than fixed array length) → skip element
///
/// For primitives (string, number, bool, null): parsed inline, returns Ok.
/// For containers (object, array): enters rd_body_to_skip which uses the
/// reserve-slot pattern for streaming across buffer boundaries.
///
/// `idx` is a local mutable index that the CALLER owns. On Ok, `*idx`
/// points past the skipped value. On Suspend/Error, ctx.idx is authoritative.
pub unsafe fn rd_parse_to_skip(
    ctx: &mut DecExecCtx,
    src: &[u8],
    idx: &mut usize,
) -> Rd {
    scanner::skip_whitespace(src, idx);
    if *idx >= src.len() {
        set_eof(ctx, *idx);
        return Rd::Suspend;
    }

    match src[*idx] {
        // String: skip inline
        b'"' => {
            *idx += 1;
            match scanner::scan_string(src, idx) {
                ScanResult::Ok(_) => {
                    ctx.idx = *idx as u32;
                    Rd::Ok
                }
                ScanResult::Eof => {
                    set_eof(ctx, *idx);
                    Rd::Suspend
                }
                ScanResult::Error(o) => {
                    set_error(ctx, o, exit::SYNTAX_ERROR);
                    Rd::Error
                }
            }
        }

        // Object: enter streaming body
        b'{' => {
            *idx += 1;
            ctx.idx = *idx as u32;
            let result = rd_body_to_skip_object(ctx, src);
            // Sync caller's idx with ctx.idx so the caller sees the final position.
            *idx = ctx.idx as usize;
            result
        }

        // Array: enter streaming body
        b'[' => {
            *idx += 1;
            ctx.idx = *idx as u32;
            let result = rd_body_to_skip_array(ctx, src);
            *idx = ctx.idx as usize;
            result
        }

        // true
        b't' => match scanner::parse_true(src, idx) {
            ScanResult::Ok(()) => {
                ctx.idx = *idx as u32;
                Rd::Ok
            }
            ScanResult::Eof => {
                set_eof(ctx, *idx);
                Rd::Suspend
            }
            ScanResult::Error(o) => {
                set_error(ctx, o, exit::SYNTAX_ERROR);
                Rd::Error
            }
        },

        // false
        b'f' => match scanner::parse_false(src, idx) {
            ScanResult::Ok(()) => {
                ctx.idx = *idx as u32;
                Rd::Ok
            }
            ScanResult::Eof => {
                set_eof(ctx, *idx);
                Rd::Suspend
            }
            ScanResult::Error(o) => {
                set_error(ctx, o, exit::SYNTAX_ERROR);
                Rd::Error
            }
        },

        // null
        b'n' => match scanner::parse_null(src, idx) {
            ScanResult::Ok(()) => {
                ctx.idx = *idx as u32;
                Rd::Ok
            }
            ScanResult::Eof => {
                set_eof(ctx, *idx);
                Rd::Suspend
            }
            ScanResult::Error(o) => {
                set_error(ctx, o, exit::SYNTAX_ERROR);
                Rd::Error
            }
        },

        // Number
        b'-' | b'0'..=b'9' => match scanner::scan_number(src, idx) {
            ScanResult::Ok(_) => {
                ctx.idx = *idx as u32;
                Rd::Ok
            }
            ScanResult::Eof => {
                set_eof(ctx, *idx);
                Rd::Suspend
            }
            ScanResult::Error(o) => {
                set_error(ctx, o, exit::SYNTAX_ERROR);
                Rd::Error
            }
        },

        _ => {
            set_error(ctx, *idx, exit::SYNTAX_ERROR);
            Rd::Error
        }
    }
}

/// Skip object body: `{ key:value, ... }` — opening `{` already consumed.
///
/// Uses reserve-slot pattern. On resume after EOF, continues from where
/// it left off — no re-scanning of previously consumed data.
///
/// Frame state: lo8 = container type ('o' for object), hi bits unused.
#[inline(never)]
pub unsafe fn rd_body_to_skip_object(ctx: &mut DecExecCtx, src: &[u8]) -> Rd {
    let my_slot = ctx.resume_depth;
    ctx.resume_depth = my_slot + 1;

    loop {
        let idx = &mut (ctx.idx as usize);
        scanner::skip_whitespace(src, idx);

        if *idx >= src.len() {
            set_eof(ctx, *idx);
            return write_skip_object_frame(ctx, my_slot);
        }

        // End of object
        if src[*idx] == b'}' {
            *idx += 1;
            ctx.idx = *idx as u32;
            ctx.resume_depth = my_slot;
            return Rd::Ok;
        }

        // Comma separator
        if src[*idx] == b',' {
            *idx += 1;
            scanner::skip_whitespace(src, idx);
        }

        // Checkpoint for rollback on leaf EOF
        let entry_start = *idx;
        ctx.idx = entry_start as u32;

        if *idx >= src.len() {
            set_eof(ctx, entry_start);
            return write_skip_object_frame(ctx, my_slot);
        }

        // Key: expect opening quote
        match scanner::expect_byte(src, idx, b'"') {
            ScanResult::Ok(()) => {}
            ScanResult::Eof => {
                set_eof(ctx, entry_start);
                return write_skip_object_frame(ctx, my_slot);
            }
            ScanResult::Error(_) => {
                set_error(ctx, *idx, exit::SYNTAX_ERROR);
                ctx.resume_depth = my_slot;
                return Rd::Error;
            }
        }

        // Key: scan string body
        match scanner::scan_string(src, idx) {
            ScanResult::Ok(_) => {}
            ScanResult::Eof => {
                set_eof(ctx, entry_start);
                return write_skip_object_frame(ctx, my_slot);
            }
            ScanResult::Error(o) => {
                set_error(ctx, o, exit::SYNTAX_ERROR);
                ctx.resume_depth = my_slot;
                return Rd::Error;
            }
        }

        // Colon
        scanner::skip_whitespace(src, idx);
        match scanner::expect_byte(src, idx, b':') {
            ScanResult::Ok(()) => {}
            ScanResult::Eof => {
                set_eof(ctx, entry_start);
                return write_skip_object_frame(ctx, my_slot);
            }
            ScanResult::Error(_) => {
                set_error(ctx, *idx, exit::SYNTAX_ERROR);
                ctx.resume_depth = my_slot;
                return Rd::Error;
            }
        }

        // Value: use streaming skip
        scanner::skip_whitespace(src, idx);
        ctx.idx = *idx as u32;
        match rd_parse_to_skip(ctx, src, idx) {
            Rd::Ok => {
                // Value skipped, continue to next entry
            }
            Rd::Suspend => {
                // Leaf suspend (no child frame pushed): roll back to entry_start
                if ctx.resume_depth == my_slot + 1 {
                    ctx.idx = entry_start as u32;
                }
                // Nested suspend: child pushed its own frame, idx set by child
                return write_skip_object_frame(ctx, my_slot);
            }
            Rd::Error => {
                ctx.resume_depth = my_slot;
                return Rd::Error;
            }
        }
    }
}

/// Skip array body: `[ value, ... ]` — opening `[` already consumed.
///
/// Uses reserve-slot pattern. Same streaming design as skip_object.
#[inline(never)]
pub unsafe fn rd_body_to_skip_array(ctx: &mut DecExecCtx, src: &[u8]) -> Rd {
    let my_slot = ctx.resume_depth;
    ctx.resume_depth = my_slot + 1;

    loop {
        let idx = &mut (ctx.idx as usize);
        scanner::skip_whitespace(src, idx);

        if *idx >= src.len() {
            set_eof(ctx, *idx);
            return write_skip_array_frame(ctx, my_slot);
        }

        // End of array
        if src[*idx] == b']' {
            *idx += 1;
            ctx.idx = *idx as u32;
            ctx.resume_depth = my_slot;
            return Rd::Ok;
        }

        // Comma separator
        if src[*idx] == b',' {
            *idx += 1;
            scanner::skip_whitespace(src, idx);
        }

        let elem_start = *idx;
        ctx.idx = elem_start as u32;

        if *idx >= src.len() {
            set_eof(ctx, elem_start);
            return write_skip_array_frame(ctx, my_slot);
        }

        // Value: use streaming skip
        match rd_parse_to_skip(ctx, src, idx) {
            Rd::Ok => {
                // Element skipped, continue
            }
            Rd::Suspend => {
                if ctx.resume_depth == my_slot + 1 {
                    ctx.idx = elem_start as u32;
                }
                return write_skip_array_frame(ctx, my_slot);
            }
            Rd::Error => {
                ctx.resume_depth = my_slot;
                return Rd::Error;
            }
        }
    }
}

/// Write SkipObjectBody frame to the reserved slot.
#[inline(always)]
unsafe fn write_skip_object_frame(ctx: &mut DecExecCtx, slot: u16) -> Rd {
    let dst = (ctx.resume_ptr as *mut ResumeFrame).add(slot as usize);
    (*dst).base = core::ptr::null_mut();
    (*dst).ti_ptr = core::ptr::null();
    (*dst).kind = resume::FrameKind::SkipObjectBody;
    (*dst).state = 0;
    Rd::Suspend
}

/// Write SkipArrayBody frame to the reserved slot.
#[inline(always)]
unsafe fn write_skip_array_frame(ctx: &mut DecExecCtx, slot: u16) -> Rd {
    let dst = (ctx.resume_ptr as *mut ResumeFrame).add(slot as usize);
    (*dst).base = core::ptr::null_mut();
    (*dst).ti_ptr = core::ptr::null();
    (*dst).kind = resume::FrameKind::SkipArrayBody;
    (*dst).state = 0;
    Rd::Suspend
}

// ============================================================
//  Struct
// ============================================================

#[inline(never)]
pub unsafe fn rd_parse_to_struct(
    ctx: &mut DecExecCtx,
    src: &[u8],
    base: *mut u8,
    desc: *const DecStructDesc,
) -> Rd {
    let idx = &mut (ctx.idx as usize);
    scanner::skip_whitespace(src, idx);

    match scanner::expect_byte(src, idx, b'{') {
        ScanResult::Ok(()) => {}
        ScanResult::Eof => {
            // EOF before '{'
            set_eof(ctx, *idx);
            resume::push_frame(
                ctx,
                ResumeFrame {
                    base,
                    ti_ptr: desc as *const DecStructDesc as *const u8,
                    kind: FrameKind::StructEntry,
                    state: 0,
                },
            );
            return Rd::Suspend;
        }
        ScanResult::Error(_) => {
            set_error(ctx, *idx, exit::SYNTAX_ERROR);
            return Rd::Error;
        }
    }
    ctx.idx = *idx as u32;

    rd_body_to_struct(ctx, src, base, desc)
}

/// Struct body loop: scan key → lookup field → rd_parse_value.
/// Uses reserve-slot pattern for suspend/resume.
#[inline(always)]
pub unsafe fn rd_body_to_struct(
    ctx: &mut DecExecCtx,
    src: &[u8],
    base: *mut u8,
    desc: *const DecStructDesc,
) -> Rd {
    let my_slot = ctx.resume_depth;
    ctx.resume_depth = my_slot + 1;
    let desc_ref = &*desc;

    loop {
        let idx = &mut (ctx.idx as usize);
        scanner::skip_whitespace(src, idx);

        if *idx >= src.len() {
            set_eof(ctx, *idx);
            return write_struct_frame(ctx, my_slot, base, desc);
        }

        if src[*idx] == b'}' {
            *idx += 1;
            ctx.idx = *idx as u32;
            ctx.resume_depth = my_slot;
            return Rd::Ok;
        }

        if src[*idx] == b',' {
            *idx += 1;
            scanner::skip_whitespace(src, idx);
        }

        let field_start = *idx;
        ctx.idx = field_start as u32;

        if *idx >= src.len() {
            set_eof(ctx, field_start);
            return write_struct_frame(ctx, my_slot, base, desc);
        }

        match scanner::expect_byte(src, idx, b'"') {
            ScanResult::Ok(()) => {}
            ScanResult::Eof => {
                set_eof(ctx, field_start);
                return write_struct_frame(ctx, my_slot, base, desc);
            }
            ScanResult::Error(_) => {
                set_error(ctx, *idx, exit::SYNTAX_ERROR);
                ctx.resume_depth = my_slot;
                return Rd::Error;
            }
        }
        let (ks, ke, _) = match scanner::scan_string(src, idx) {
            ScanResult::Ok(v) => v,
            ScanResult::Eof => {
                set_eof(ctx, field_start);
                return write_struct_frame(ctx, my_slot, base, desc);
            }
            ScanResult::Error(o) => {
                set_error(ctx, o, exit::SYNTAX_ERROR);
                ctx.resume_depth = my_slot;
                return Rd::Error;
            }
        };
        let key = &src[ks..ke];

        scanner::skip_whitespace(src, idx);
        match scanner::expect_byte(src, idx, b':') {
            ScanResult::Ok(()) => {}
            ScanResult::Eof => {
                set_eof(ctx, field_start);
                return write_struct_frame(ctx, my_slot, base, desc);
            }
            ScanResult::Error(_) => {
                set_error(ctx, *idx, exit::SYNTAX_ERROR);
                ctx.resume_depth = my_slot;
                return Rd::Error;
            }
        }
        scanner::skip_whitespace(src, idx);
        ctx.idx = *idx as u32;

        match desc_ref.find_field(key) {
            Some(field) => {
                let target = base.add(field.offset as usize);
                match rd_parse_value(ctx, src, field.val_kind, field.val_desc, target) {
                    Rd::Ok => {}
                    Rd::Suspend => {
                        // Leaf: roll back to field_start.
                        // Nested child: idx already set by child.
                        if ctx.resume_depth == my_slot + 1 {
                            ctx.idx = field_start as u32;
                        }
                        return write_struct_frame(ctx, my_slot, base, desc);
                    }
                    Rd::Error => {
                        ctx.resume_depth = my_slot;
                        return Rd::Error;
                    }
                }
            }
            None => {
                let idx = &mut (ctx.idx as usize);
                match rd_parse_to_skip(ctx, src, idx) {
                    Rd::Ok => {
                        ctx.idx = *idx as u32;
                    }
                    Rd::Suspend => {
                        // Leaf: roll back to field_start.
                        // Nested: child pushed its own frame, idx set by child.
                        if ctx.resume_depth == my_slot + 1 {
                            ctx.idx = field_start as u32;
                        }
                        return write_struct_frame(ctx, my_slot, base, desc);
                    }
                    Rd::Error => {
                        ctx.resume_depth = my_slot;
                        return Rd::Error;
                    }
                }
            }
        }
    }
}

/// Write StructBody frame to the reserved slot.
#[inline(always)]
unsafe fn write_struct_frame(
    ctx: &mut DecExecCtx,
    slot: u16,
    base: *mut u8,
    desc: *const DecStructDesc,
) -> Rd {
    let dst = (ctx.resume_ptr as *mut ResumeFrame).add(slot as usize);
    (*dst).base = base;
    (*dst).ti_ptr = desc as *const u8;
    (*dst).kind = FrameKind::StructBody;
    Rd::Suspend
}

// ============================================================
//  Slice
// ============================================================

#[inline(never)]
pub unsafe fn rd_parse_to_slice(
    ctx: &mut DecExecCtx,
    src: &[u8],
    target: *mut u8,
    desc: *const DecSliceDesc,
) -> Rd {
    let idx = &mut (ctx.idx as usize);
    scanner::skip_whitespace(src, idx);

    match scanner::expect_byte(src, idx, b'[') {
        ScanResult::Ok(()) => {}
        ScanResult::Eof => {
            set_eof(ctx, *idx);
            resume::push_frame(
                ctx,
                ResumeFrame {
                    base: target,
                    ti_ptr: desc as *const DecSliceDesc as *const u8,
                    kind: FrameKind::SliceEntry,
                    state: 0,
                },
            );
            return Rd::Suspend;
        }
        ScanResult::Error(_) => {
            set_error(ctx, *idx, exit::SYNTAX_ERROR);
            return Rd::Error;
        }
    }
    ctx.idx = *idx as u32;

    rd_parse_to_slice_init(ctx, src, target, desc)
}

/// '[' consumed — determine empty vs non-empty, yield for allocation.
pub unsafe fn rd_parse_to_slice_init(
    ctx: &mut DecExecCtx,
    src: &[u8],
    target: *mut u8,
    desc: *const DecSliceDesc,
) -> Rd {
    let idx = &mut (ctx.idx as usize);
    scanner::skip_whitespace(src, idx);

    if *idx >= src.len() {
        ctx.idx = *idx as u32;
        set_eof(ctx, *idx);
        resume::push_frame(
            ctx,
            ResumeFrame {
                base: target,
                ti_ptr: desc as *const DecSliceDesc as *const u8,
                kind: FrameKind::SliceInit,
                state: 0,
            },
        );
        return Rd::Suspend;
    }
    if src[*idx] == b']' {
        *idx += 1;
        ctx.idx = *idx as u32;
        writer::write_zero(target, 24); // zero SliceHeader
        return Rd::Ok;
    }

    let desc_ref = &*desc;
    ctx.exit_code = exit::YIELD_ALLOC_SLICE;
    ctx.yield_param0 = desc_ref.codec_ptr as u64;
    ctx.yield_param1 = target as u64;
    ctx.yield_param2 = desc_ref.elem_rtype as u64;

    resume::push_frame(
        ctx,
        ResumeFrame {
            base: target,
            ti_ptr: desc as *const DecSliceDesc as *const u8,
            kind: FrameKind::SliceBody,
            state: 0,
        },
    );
    Rd::Suspend
}

/// Slice body: parse elements until ']'.
/// count/cap persisted in ResumeFrame.state (not yield_param)
/// so nested suspends cannot corrupt them.
#[inline(always)]
pub unsafe fn rd_body_to_slice(
    ctx: &mut DecExecCtx,
    src: &[u8],
    target: *mut u8,
    desc: *const DecSliceDesc,
) -> Rd {
    let my_slot = ctx.resume_depth;
    ctx.resume_depth = my_slot + 1;
    let desc_ref = &*desc;
    let elem_size = desc_ref.elem_size as usize;
    let elem_kind = desc_ref.elem_kind;
    let elem_desc = desc_ref.elem_desc;
    let base = ctx.cur_base;
    // Read count+cap from the resume frame (immune to nested yield overwrites).
    let frame = &*(ctx.resume_ptr as *const ResumeFrame).add(my_slot as usize);
    let (raw_count, saved_cap) = frame.get_count_cap();
    let value_done = (raw_count & 0x8000_0000) != 0;
    let mut count = (raw_count & 0x7FFF_FFFF) as usize;
    // On first entry (after YIELD_ALLOC_SLICE) and after YIELD_GROW_SLICE,
    // Go writes the fresh capacity to yield_param1 right before Resume().
    // We pick it up here; once stored in the frame, we never re-read the
    // yield register — nested yields may overwrite it freely.
    let cap = if saved_cap == 0 {
        ctx.yield_param1 as usize  // first entry or after grow
    } else {
        saved_cap as usize         // resumed from EOF/nested suspend
    };

    if value_done {
        count += 1; // nested child completed via resume
    }

    loop {
        let idx = &mut (ctx.idx as usize);
        scanner::skip_whitespace(src, idx);

        if *idx >= src.len() {
            set_eof(ctx, *idx);
            return write_slice_frame(ctx, my_slot, target, desc, count as u32, cap as u32);
        }

        if src[*idx] == b']' {
            *idx += 1;
            ctx.idx = *idx as u32;
            // Write Len directly into the Go slice header (offset 8).
            // Len is int (non-pointer), so no GC write barrier needed.
            // This avoids relying on yield_param0 which may be overwritten
            // by subsequent map/slice operations before Go reads it.
            core::ptr::write_unaligned(target.add(8) as *mut usize, count);
            ctx.resume_depth = my_slot;
            return Rd::Ok;
        }

        if src[*idx] == b',' {
            *idx += 1;
            scanner::skip_whitespace(src, idx);
        }

        let elem_start = *idx;
        ctx.idx = elem_start as u32;

        if *idx >= src.len() {
            set_eof(ctx, elem_start);
            return write_slice_frame(ctx, my_slot, target, desc, count as u32, cap as u32);
        }

        if count >= cap {
            ctx.yield_param0 = count as u64;
            ctx.yield_param1 = target as u64;
            ctx.yield_param2 = desc_ref.elem_rtype as u64;
            ctx.exit_code = exit::YIELD_GROW_SLICE;
            // Save cap=0 so that on resume, rd_body_to_slice reads the
            // fresh capacity from yield_param1 (set by Go grow handler).
            return write_slice_frame(ctx, my_slot, target, desc, count as u32, 0);
        }

        let elem_ptr = base.add(count * elem_size);

        match rd_parse_value(ctx, src, elem_kind, elem_desc, elem_ptr) {
            Rd::Ok => {
                count += 1;
            }
            Rd::Suspend => {
                if ctx.resume_depth == my_slot + 1 {
                    ctx.idx = elem_start as u32;
                    return write_slice_frame(ctx, my_slot, target, desc, count as u32, cap as u32);
                } else {
                    return write_slice_frame(
                        ctx,
                        my_slot,
                        target,
                        desc,
                        count as u32 | 0x8000_0000,
                        cap as u32,
                    );
                }
            }
            Rd::Error => {
                ctx.resume_depth = my_slot;
                return Rd::Error;
            }
        }
    }
}

/// Write SliceBody frame to the reserved slot.
#[inline(always)]
unsafe fn write_slice_frame(
    ctx: &mut DecExecCtx,
    slot: u16,
    target: *mut u8,
    desc: *const DecSliceDesc,
    count: u32,
    cap: u32,
) -> Rd {
    let dst = (ctx.resume_ptr as *mut ResumeFrame).add(slot as usize);
    (*dst).base = target;
    (*dst).ti_ptr = desc as *const u8;
    (*dst).kind = FrameKind::SliceBody;
    (*dst).set_count_cap(count, cap);
    Rd::Suspend
}

// ============================================================
//  Array (fixed-length Go [N]T)
// ============================================================

/// Consume '[', then enter body loop. No allocation yield needed —
/// memory is inline in the parent struct at `target`.
#[inline(never)]
pub unsafe fn rd_parse_to_array(
    ctx: &mut DecExecCtx,
    src: &[u8],
    target: *mut u8,
    desc: *const DecArrayDesc,
) -> Rd {
    let idx = &mut (ctx.idx as usize);
    scanner::skip_whitespace(src, idx);

    match scanner::expect_byte(src, idx, b'[') {
        ScanResult::Ok(()) => {}
        ScanResult::Eof => {
            set_eof(ctx, *idx);
            resume::push_frame(
                ctx,
                ResumeFrame {
                    base: target,
                    ti_ptr: desc as *const u8,
                    kind: FrameKind::ArrayEntry,
                    state: 0,
                },
            );
            return Rd::Suspend;
        }
        ScanResult::Error(o) => {
            set_error(ctx, o, exit::SYNTAX_ERROR);
            return Rd::Error;
        }
    }
    ctx.idx = *idx as u32;

    // Zero the frame at the slot rd_body_to_array will reserve,
    // so it reads count=0 on first entry (not stale data).
    let slot = ctx.resume_depth;
    let frame = (ctx.resume_ptr as *mut ResumeFrame).add(slot as usize);
    (*frame).state = 0;

    rd_body_to_array(ctx, src, target, desc)
}

/// Array body: parse up to N elements into inline memory at `target`.
/// Overflow elements are skipped. Uses reserve-slot pattern.
/// count stored in ResumeFrame.state lo31, bit31 = value-in-progress.
#[inline(always)]
pub unsafe fn rd_body_to_array(
    ctx: &mut DecExecCtx,
    src: &[u8],
    target: *mut u8,
    desc: *const DecArrayDesc,
) -> Rd {
    let my_slot = ctx.resume_depth;
    ctx.resume_depth = my_slot + 1;
    let desc_ref = &*desc;
    let elem_size = desc_ref.elem_size as usize;
    let elem_kind = desc_ref.elem_kind;
    let elem_desc = desc_ref.elem_desc;
    let cap = desc_ref.array_len as usize;

    // Read count from frame. On first entry the frame state was zeroed
    // by rd_parse_to_array. On resume it carries the saved count.
    let frame = &*(ctx.resume_ptr as *const ResumeFrame).add(my_slot as usize);
    let raw_count = frame.get_count();
    let value_done = (raw_count & 0x8000_0000) != 0;
    let mut count = (raw_count & 0x7FFF_FFFF) as usize;

    if value_done {
        count += 1; // nested child completed via resume
    }

    loop {
        let idx = &mut (ctx.idx as usize);
        scanner::skip_whitespace(src, idx);

        if *idx >= src.len() {
            set_eof(ctx, *idx);
            return write_array_frame(ctx, my_slot, target, desc, count as u32);
        }

        if src[*idx] == b']' {
            *idx += 1;
            ctx.idx = *idx as u32;
            ctx.resume_depth = my_slot;
            return Rd::Ok;
        }

        if src[*idx] == b',' {
            *idx += 1;
            scanner::skip_whitespace(src, idx);
        }

        let elem_start = *idx;
        ctx.idx = elem_start as u32;

        if *idx >= src.len() {
            set_eof(ctx, elem_start);
            return write_array_frame(ctx, my_slot, target, desc, count as u32);
        }

        // Overflow: skip elements beyond array length
        if count >= cap {
            match rd_parse_to_skip(ctx, src, idx) {
                Rd::Ok => {
                    ctx.idx = *idx as u32;
                    continue;
                }
                Rd::Suspend => {
                    if ctx.resume_depth == my_slot + 1 {
                        ctx.idx = elem_start as u32;
                    }
                    return write_array_frame(ctx, my_slot, target, desc, count as u32);
                }
                Rd::Error => {
                    ctx.resume_depth = my_slot;
                    return Rd::Error;
                }
            }
        }

        let elem_ptr = target.add(count * elem_size);

        match rd_parse_value(ctx, src, elem_kind, elem_desc, elem_ptr) {
            Rd::Ok => {
                count += 1;
            }
            Rd::Suspend => {
                if ctx.resume_depth == my_slot + 1 {
                    // Leaf: roll back to elem_start
                    ctx.idx = elem_start as u32;
                    return write_array_frame(ctx, my_slot, target, desc, count as u32);
                } else {
                    // Nested child pushed frames; set value-in-progress flag
                    return write_array_frame(
                        ctx,
                        my_slot,
                        target,
                        desc,
                        count as u32 | 0x8000_0000,
                    );
                }
            }
            Rd::Error => {
                ctx.resume_depth = my_slot;
                return Rd::Error;
            }
        }
    }
}

/// Write ArrayBody frame to the reserved slot.
#[inline(always)]
unsafe fn write_array_frame(
    ctx: &mut DecExecCtx,
    slot: u16,
    target: *mut u8,
    desc: *const DecArrayDesc,
    count: u32,
) -> Rd {
    let dst = (ctx.resume_ptr as *mut ResumeFrame).add(slot as usize);
    (*dst).base = target;
    (*dst).ti_ptr = desc as *const u8;
    (*dst).kind = FrameKind::ArrayBody;
    (*dst).set_count(count);
    Rd::Suspend
}

// ============================================================
//  Map
// ============================================================

#[inline(never)]
pub unsafe fn rd_parse_to_map(
    ctx: &mut DecExecCtx,
    src: &[u8],
    target: *mut u8,
    desc: *const DecMapDesc,
) -> Rd {
    let idx = &mut (ctx.idx as usize);
    scanner::skip_whitespace(src, idx);

    match scanner::expect_byte(src, idx, b'{') {
        ScanResult::Ok(()) => {}
        ScanResult::Eof => {
            set_eof(ctx, *idx);
            resume::push_frame(
                ctx,
                ResumeFrame {
                    base: target,
                    ti_ptr: desc as *const DecMapDesc as *const u8,
                    kind: FrameKind::MapEntry,
                    state: 0,
                },
            );
            return Rd::Suspend;
        }
        ScanResult::Error(_) => {
            set_error(ctx, *idx, exit::SYNTAX_ERROR);
            return Rd::Error;
        }
    }
    ctx.idx = *idx as u32;

    rd_parse_to_map_init(ctx, src, target, desc)
}

/// '{' consumed — determine empty vs non-empty, yield MAP_INIT.
pub unsafe fn rd_parse_to_map_init(
    ctx: &mut DecExecCtx,
    src: &[u8],
    target: *mut u8,
    desc: *const DecMapDesc,
) -> Rd {
    let idx = &mut (ctx.idx as usize);
    scanner::skip_whitespace(src, idx);

    if *idx >= src.len() {
        ctx.idx = *idx as u32;
        set_eof(ctx, *idx);
        resume::push_frame(
            ctx,
            ResumeFrame {
                base: target,
                ti_ptr: desc as *const DecMapDesc as *const u8,
                kind: FrameKind::MapInit,
                state: 0,
            },
        );
        return Rd::Suspend;
    }
    if src[*idx] == b'}' {
        *idx += 1;
        ctx.idx = *idx as u32;
        let desc_ref = &*desc;
        ctx.exit_code = exit::YIELD_MAP_INIT;
        ctx.yield_param0 = desc_ref.codec_ptr as u64;
        ctx.yield_param1 = target as u64;
        ctx.yield_param2 = 1; // empty=true; Go sets buf_cap=0 → rd_body_to_map returns Ok
        resume::push_frame(
            ctx,
            ResumeFrame {
                base: target,
                ti_ptr: desc as *const DecMapDesc as *const u8,
                kind: FrameKind::MapBody,
                state: 0,
            },
        );
        return Rd::Suspend;
    }

    let desc_ref = &*desc;
    ctx.exit_code = exit::YIELD_MAP_INIT;
    ctx.yield_param0 = desc_ref.codec_ptr as u64;
    ctx.yield_param1 = target as u64;
    ctx.yield_param2 = 0; // empty=false
    resume::push_frame(
        ctx,
        ResumeFrame {
            base: target,
            ti_ptr: desc as *const DecMapDesc as *const u8,
            kind: FrameKind::MapBody,
            state: 0,
        },
    );
    Rd::Suspend
}

/// Map body loop: parse entries into batch buffer, yield MAP_ASSIGN when
/// full or done. Uses reserve-slot pattern.
///
/// Entry buffer stride = 16 + val_size:  [key_ptr:8][key_len:8][value:val_size]
#[inline(always)]
pub unsafe fn rd_body_to_map(
    ctx: &mut DecExecCtx,
    src: &[u8],
    target: *mut u8,
    desc: *const DecMapDesc,
) -> Rd {
    let my_slot = ctx.resume_depth;
    ctx.resume_depth = my_slot + 1;
    let desc_ref = &*desc;
    let entry_buf = ctx.cur_base;
    let val_size = desc_ref.val_size as usize;
    let stride = 16 + val_size;
    // Read count + buf_cap from frame (immune to nested yield overwrites).
    let frame = &*(ctx.resume_ptr as *const ResumeFrame).add(my_slot as usize);
    let (raw_state, saved_cap) = frame.get_count_cap();
    let value_done = (raw_state & 0x8000_0000) != 0;
    let mut count = (raw_state & 0x7FFF_FFFF) as usize;
    let buf_cap = if saved_cap == 0 {
        // First entry: read from yield_param1 (set by Go MAP_INIT handler).
        // buf_cap==0 from Go means empty map — early exit handled below.
        ctx.yield_param1 as usize
    } else {
        saved_cap as usize
    };

    if buf_cap == 0 {
        ctx.resume_depth = my_slot;
        return Rd::Ok;
    }

    if value_done {
        count += 1; // nested child completed via resume
    }

    loop {
        let idx = &mut (ctx.idx as usize);
        scanner::skip_whitespace(src, idx);

        if *idx >= src.len() {
            set_eof(ctx, *idx);
            ctx.yield_param0 = count as u64;
            return write_map_frame(ctx, my_slot, target, desc, count as u32, buf_cap as u32);
        }

        if src[*idx] == b'}' {
            *idx += 1;
            ctx.idx = *idx as u32;
            if count > 0 {
                ctx.yield_param0 = count as u64;
                ctx.yield_param2 = 1; // done=true
                ctx.exit_code = exit::YIELD_MAP_ASSIGN;
                ctx.resume_depth = my_slot;
                return Rd::Suspend;
            }
            ctx.resume_depth = my_slot;
            return Rd::Ok;
        }

        if src[*idx] == b',' {
            *idx += 1;
            scanner::skip_whitespace(src, idx);
        }

        let entry_start = *idx;
        ctx.idx = entry_start as u32;

        if *idx >= src.len() {
            set_eof(ctx, entry_start);
            ctx.yield_param0 = count as u64;
            return write_map_frame(ctx, my_slot, target, desc, count as u32, buf_cap as u32);
        }

        if count >= buf_cap {
            ctx.yield_param0 = count as u64;
            ctx.yield_param2 = 0; // done=false
            ctx.exit_code = exit::YIELD_MAP_ASSIGN;
            return write_map_frame(ctx, my_slot, target, desc, count as u32, buf_cap as u32);
        }

        let entry_ptr = entry_buf.add(count * stride);
        let val_ptr = entry_ptr.add(16);

        match scanner::expect_byte(src, idx, b'"') {
            ScanResult::Ok(()) => {}
            ScanResult::Eof => {
                set_eof(ctx, entry_start);
                ctx.yield_param0 = count as u64;
                return write_map_frame(ctx, my_slot, target, desc, count as u32, buf_cap as u32);
            }
            ScanResult::Error(_) => {
                set_error(ctx, *idx, exit::SYNTAX_ERROR);
                ctx.resume_depth = my_slot;
                return Rd::Error;
            }
        }
        let (ks, ke, has_escape) = match scanner::scan_string(src, idx) {
            ScanResult::Ok(v) => v,
            ScanResult::Eof => {
                set_eof(ctx, entry_start);
                ctx.yield_param0 = count as u64;
                return write_map_frame(ctx, my_slot, target, desc, count as u32, buf_cap as u32);
            }
            ScanResult::Error(o) => {
                set_error(ctx, o, exit::SYNTAX_ERROR);
                ctx.resume_depth = my_slot;
                return Rd::Error;
            }
        };

        if !has_escape {
            let key_ptr = src.as_ptr().add(ks);
            core::ptr::write_unaligned(entry_ptr as *mut usize, key_ptr as usize);
            core::ptr::write_unaligned((entry_ptr as *mut usize).add(1), ke - ks);
        } else {
            let raw_len = ke - ks;
            let arena_used = ctx.scratch_len as usize;
            let arena_cap = ctx.scratch_cap as usize;

            if arena_used + raw_len > arena_cap {
                ctx.idx = entry_start as u32;
                ctx.yield_param0 = count as u64;
                return write_map_frame(ctx, my_slot, target, desc, count as u32, buf_cap as u32);
            }

            let arena = core::slice::from_raw_parts_mut(
                ctx.scratch_ptr.add(arena_used),
                arena_cap - arena_used,
            );
            let raw = &src[ks..ke];
            let written = scanner::unescape_to(raw, arena);
            let str_ptr = ctx.scratch_ptr.add(arena_used);
            core::ptr::write_unaligned(entry_ptr as *mut usize, str_ptr as usize);
            core::ptr::write_unaligned((entry_ptr as *mut usize).add(1), written);
            ctx.scratch_len = (arena_used + written) as u32;
        }

        scanner::skip_whitespace(src, idx);
        match scanner::expect_byte(src, idx, b':') {
            ScanResult::Ok(()) => {}
            ScanResult::Eof => {
                set_eof(ctx, entry_start);
                ctx.yield_param0 = count as u64;
                return write_map_frame(ctx, my_slot, target, desc, count as u32, buf_cap as u32);
            }
            ScanResult::Error(_) => {
                set_error(ctx, *idx, exit::SYNTAX_ERROR);
                ctx.resume_depth = my_slot;
                return Rd::Error;
            }
        }
        scanner::skip_whitespace(src, idx);
        ctx.idx = *idx as u32;

        match rd_parse_value(ctx, src, desc_ref.val_kind, desc_ref.val_desc, val_ptr) {
            Rd::Ok => {
                count += 1;
            }
            Rd::Suspend => {
                if ctx.resume_depth == my_slot + 1 {
                    ctx.idx = entry_start as u32;
                    ctx.yield_param0 = count as u64;
                    return write_map_frame(ctx, my_slot, target, desc, count as u32, buf_cap as u32);
                } else {
                    ctx.yield_param0 = count as u64;
                    return write_map_frame(ctx, my_slot, target, desc, count as u32 | 0x8000_0000, buf_cap as u32);
                }
            }
            Rd::Error => {
                ctx.resume_depth = my_slot;
                return Rd::Error;
            }
        }
    }
}

/// Write MapBody frame to the reserved slot.
#[inline(always)]
unsafe fn write_map_frame(
    ctx: &mut DecExecCtx,
    slot: u16,
    target: *mut u8,
    desc: *const DecMapDesc,
    count: u32,
    buf_cap: u32,
) -> Rd {
    let dst = (ctx.resume_ptr as *mut ResumeFrame).add(slot as usize);
    (*dst).base = target;
    (*dst).ti_ptr = desc as *const u8;
    (*dst).kind = FrameKind::MapBody;
    (*dst).set_count_cap(count, buf_cap);
    Rd::Suspend
}

// ============================================================
//  Tape-mode: interface{} / any
// ============================================================
//
// For kind::ANY targets, emit instructions instead of writing
// directly to memory. Go executes the instructions to construct
// efaces and allocate containers.

/// Emit instruction for a JSON value. Caller must have skipped
/// whitespace and checked EOF.
pub unsafe fn rd_parse_to_any(ctx: &mut DecExecCtx, src: &[u8]) -> Rd {
    let idx = &mut (ctx.idx as usize);

    match src[*idx] {
        b'n' => match scanner::parse_null(src, idx) {
            ScanResult::Ok(()) => {
                ctx.idx = *idx as u32;
                insn::insn_emit_null(ctx);
                Rd::Ok
            }
            ScanResult::Eof => {
                set_eof(ctx, *idx);
                Rd::Suspend
            }
            ScanResult::Error(o) => {
                set_error(ctx, o, exit::SYNTAX_ERROR);
                Rd::Error
            }
        },
        b't' => match scanner::parse_true(src, idx) {
            ScanResult::Ok(()) => {
                ctx.idx = *idx as u32;
                insn::insn_emit_true(ctx);
                Rd::Ok
            }
            ScanResult::Eof => {
                set_eof(ctx, *idx);
                Rd::Suspend
            }
            ScanResult::Error(o) => {
                set_error(ctx, o, exit::SYNTAX_ERROR);
                Rd::Error
            }
        },
        b'f' => match scanner::parse_false(src, idx) {
            ScanResult::Ok(()) => {
                ctx.idx = *idx as u32;
                insn::insn_emit_false(ctx);
                Rd::Ok
            }
            ScanResult::Eof => {
                set_eof(ctx, *idx);
                Rd::Suspend
            }
            ScanResult::Error(o) => {
                set_error(ctx, o, exit::SYNTAX_ERROR);
                Rd::Error
            }
        },
        b'"' => {
            *idx += 1;
            match scanner::scan_string(src, idx) {
                ScanResult::Ok((start, end, has_escape)) => {
                    ctx.idx = *idx as u32;
                    if !has_escape {
                        let ptr = src.as_ptr().add(start);
                        insn::insn_emit_string(ctx, ptr, (end - start) as u32);
                    } else {
                        let raw_len = end - start;
                        let arena_used = ctx.scratch_len as usize;
                        let arena_cap = ctx.scratch_cap as usize;

                        if arena_used + raw_len > arena_cap {
                            ctx.exit_code = exit::YIELD_ARENA_FULL;
                            ctx.yield_param0 = raw_len as u64;
                            return Rd::Suspend;
                        }
                        let arena = core::slice::from_raw_parts_mut(
                            ctx.scratch_ptr.add(arena_used),
                            arena_cap - arena_used,
                        );
                        let raw = &src[start..end];
                        let written = scanner::unescape_to(raw, arena);
                        insn::insn_emit_string_esc(ctx, arena_used as u32, written as u32);
                        ctx.scratch_len = (arena_used + written) as u32;
                    }
                    Rd::Ok
                }
                ScanResult::Eof => {
                    set_eof(ctx, *idx);
                    Rd::Suspend
                }
                ScanResult::Error(o) => {
                    set_error(ctx, o, exit::SYNTAX_ERROR);
                    Rd::Error
                }
            }
        }
        b'-' | b'0'..=b'9' => match scanner::parse_int64(src, idx) {
            ScanResult::Ok(v) => {
                ctx.idx = *idx as u32;
                insn::insn_emit_int(ctx, v);
                return Rd::Ok;
            }
            ScanResult::Eof => {
                set_eof(ctx, *idx);
                return Rd::Suspend;
            }
            ScanResult::Error(_) => {
                *idx = ctx.idx as usize;
                match scanner::scan_number(src, idx) {
                    ScanResult::Ok((start, end)) => {
                        ctx.idx = *idx as u32;
                        let ptr = src.as_ptr().add(start);
                        insn::insn_emit_number(ctx, ptr, (end - start) as u32);
                        return Rd::Ok;
                    }
                    ScanResult::Eof => {
                        set_eof(ctx, *idx);
                        return Rd::Suspend;
                    }
                    ScanResult::Error(o) => {
                        set_error(ctx, o, exit::SYNTAX_ERROR);
                        return Rd::Error;
                    }
                }
            }
        },
        b'{' => {
            *idx += 1;
            ctx.idx = *idx as u32;
            rd_parse_to_any_object(ctx, src, core::ptr::null_mut())
        }
        b'[' => {
            *idx += 1;
            ctx.idx = *idx as u32;
            rd_parse_to_any_array(ctx, src, core::ptr::null_mut())
        }
        _ => {
            set_error(ctx, *idx, exit::SYNTAX_ERROR);
            Rd::Error
        }
    }
}

/// Tape-mode object: MAKE_OBJECT → (SET_KEY + value)* → CLOSE_OBJECT.
/// `target` non-null = top-level (emit SET_TARGET); null = nested.
#[inline(never)]
pub unsafe fn rd_parse_to_any_object(ctx: &mut DecExecCtx, src: &[u8], target: *mut u8) -> Rd {
    if !target.is_null() {
        insn::insn_emit_set_target(ctx, target as u64);
    }
    insn::insn_emit_make_object(ctx, 0);

    rd_body_to_any_object(ctx, src, target)
}

/// Any-object body loop. Separated from entry so resume skips the
/// duplicate MAKE_OBJECT emission.
#[inline(always)]
pub unsafe fn rd_body_to_any_object(ctx: &mut DecExecCtx, src: &[u8], target: *mut u8) -> Rd {
    let my_slot = ctx.resume_depth;
    ctx.resume_depth = my_slot + 1;

    loop {
        let idx = &mut (ctx.idx as usize);
        scanner::skip_whitespace(src, idx);

        if *idx >= src.len() {
            set_eof(ctx, *idx);
            return write_any_object_frame(ctx, my_slot, target);
        }

        if src[*idx] == b'}' {
            *idx += 1;
            ctx.idx = *idx as u32;
            insn::insn_emit_close_object(ctx);
            if insn::insn_flush_if_needed(ctx) {
                // Tape nearly full after CLOSE — yield so Go flushes,
                // then resume will see depth popped → Ok.
                ctx.resume_depth = my_slot;
                return Rd::Suspend;
            }
            ctx.resume_depth = my_slot;
            return Rd::Ok;
        }

        if src[*idx] == b',' {
            *idx += 1;
            scanner::skip_whitespace(src, idx);
        }

        if insn::insn_near_full(ctx) {
            ctx.exit_code = exit::YIELD_INSN_FLUSH;
            return write_any_object_frame(ctx, my_slot, target);
        }

        if *idx >= src.len() {
            set_eof(ctx, *idx);
            return write_any_object_frame(ctx, my_slot, target);
        }

        let key_start = *idx;
        ctx.idx = key_start as u32;
        // Save insn position so we can roll back if this entry is
        // interrupted by EOF. After refill the whole key+value pair
        // will be re-parsed, so we must not leave partial SET_KEY
        // instructions in the buffer.
        let insn_save = ctx.insn_len;

        match scanner::expect_byte(src, idx, b'"') {
            ScanResult::Ok(()) => {}
            ScanResult::Eof => {
                set_eof(ctx, key_start);
                ctx.insn_len = insn_save;
                return write_any_object_frame(ctx, my_slot, target);
            }
            ScanResult::Error(_) => {
                set_error(ctx, *idx, exit::SYNTAX_ERROR);
                ctx.resume_depth = my_slot;
                return Rd::Error;
            }
        }

        let (ks, ke, key_esc) = match scanner::scan_string(src, idx) {
            ScanResult::Ok(r) => r,
            ScanResult::Eof => {
                set_eof(ctx, key_start);
                ctx.insn_len = insn_save;
                return write_any_object_frame(ctx, my_slot, target);
            }
            ScanResult::Error(o) => {
                set_error(ctx, o, exit::SYNTAX_ERROR);
                ctx.resume_depth = my_slot;
                return Rd::Error;
            }
        };
        ctx.idx = *idx as u32;

        if !key_esc {
            let ptr = src.as_ptr().add(ks);
            insn::insn_emit_set_key(ctx, ptr, (ke - ks) as u32);
        } else {
            let raw_len = ke - ks;
            let arena_used = ctx.scratch_len as usize;
            let arena_cap = ctx.scratch_cap as usize;

            if arena_used + raw_len > arena_cap {
                ctx.exit_code = exit::YIELD_ARENA_FULL;
                ctx.yield_param0 = raw_len as u64;
                ctx.insn_len = insn_save;
                ctx.idx = key_start as u32;
                return write_any_object_frame(ctx, my_slot, target);
            }
            let arena = core::slice::from_raw_parts_mut(
                ctx.scratch_ptr.add(arena_used),
                arena_cap - arena_used,
            );
            let raw = &src[ks..ke];
            let written = scanner::unescape_to(raw, arena);
            insn::insn_emit_set_key(ctx, ctx.scratch_ptr.add(arena_used), written as u32);
            ctx.scratch_len = (arena_used + written) as u32;
        }

        scanner::skip_whitespace(src, idx);
        match scanner::expect_byte(src, idx, b':') {
            ScanResult::Ok(()) => {}
            ScanResult::Eof => {
                set_eof(ctx, key_start);
                ctx.insn_len = insn_save;
                return write_any_object_frame(ctx, my_slot, target);
            }
            ScanResult::Error(_) => {
                set_error(ctx, *idx, exit::SYNTAX_ERROR);
                ctx.resume_depth = my_slot;
                return Rd::Error;
            }
        }

        scanner::skip_whitespace(src, idx);
        if *idx >= src.len() {
            set_eof(ctx, key_start);
            ctx.insn_len = insn_save;
            return write_any_object_frame(ctx, my_slot, target);
        }

        ctx.idx = *idx as u32;
        match rd_parse_to_any(ctx, src) {
            Rd::Ok => continue,
            Rd::Suspend => {
                // Leaf suspend (e.g. EOF mid-string): roll back entire entry
                // so key+value is re-parsed after refill.
                if ctx.resume_depth == my_slot + 1 {
                    ctx.idx = key_start as u32;
                    ctx.insn_len = insn_save;
                }
                // Nested suspend (child pushed frames): don't roll back,
                // the child will resume from its own frame.
                return write_any_object_frame(ctx, my_slot, target);
            }
            Rd::Error => {
                ctx.resume_depth = my_slot;
                return Rd::Error;
            }
        }
    }
}

/// Tape-mode array: MAKE_ARRAY → value* → CLOSE_ARRAY.
#[inline(never)]
pub unsafe fn rd_parse_to_any_array(ctx: &mut DecExecCtx, src: &[u8], target: *mut u8) -> Rd {
    if !target.is_null() {
        insn::insn_emit_set_target(ctx, target as u64);
    }
    insn::insn_emit_make_array(ctx, 0);

    rd_body_to_any_array(ctx, src, target)
}

/// Any-array body loop. Separated from entry so resume skips the
/// duplicate MAKE_ARRAY emission.
#[inline(always)]
pub unsafe fn rd_body_to_any_array(ctx: &mut DecExecCtx, src: &[u8], target: *mut u8) -> Rd {
    let my_slot = ctx.resume_depth;
    ctx.resume_depth = my_slot + 1;

    loop {
        let idx = &mut (ctx.idx as usize);
        scanner::skip_whitespace(src, idx);

        if *idx >= src.len() {
            set_eof(ctx, *idx);
            return write_any_array_frame(ctx, my_slot, target);
        }

        if src[*idx] == b']' {
            *idx += 1;
            ctx.idx = *idx as u32;
            insn::insn_emit_close_array(ctx);
            if insn::insn_flush_if_needed(ctx) {
                ctx.resume_depth = my_slot;
                return Rd::Suspend;
            }
            ctx.resume_depth = my_slot;
            return Rd::Ok;
        }

        if src[*idx] == b',' {
            *idx += 1;
            scanner::skip_whitespace(src, idx);
        }

        if insn::insn_near_full(ctx) {
            ctx.exit_code = exit::YIELD_INSN_FLUSH;
            return write_any_array_frame(ctx, my_slot, target);
        }

        if *idx >= src.len() {
            set_eof(ctx, *idx);
            return write_any_array_frame(ctx, my_slot, target);
        }

        let elem_start = *idx;
        ctx.idx = *idx as u32;
        match rd_parse_to_any(ctx, src) {
            Rd::Ok => continue,
            Rd::Suspend => {
                // Leaf suspend: roll back so element is re-parsed after refill.
                if ctx.resume_depth == my_slot + 1 {
                    ctx.idx = elem_start as u32;
                }
                return write_any_array_frame(ctx, my_slot, target);
            }
            Rd::Error => {
                ctx.resume_depth = my_slot;
                return Rd::Error;
            }
        }
    }
}

/// Write AnyObjectBody frame to the reserved slot.
#[inline(always)]
unsafe fn write_any_object_frame(ctx: &mut DecExecCtx, slot: u16, target: *mut u8) -> Rd {
    let dst = (ctx.resume_ptr as *mut ResumeFrame).add(slot as usize);
    (*dst).base = target;
    (*dst).ti_ptr = core::ptr::null();
    (*dst).kind = FrameKind::AnyObjectBody;
    (*dst).state = 0;
    Rd::Suspend
}

/// Write AnyArrayBody frame to the reserved slot.
#[inline(always)]
unsafe fn write_any_array_frame(ctx: &mut DecExecCtx, slot: u16, target: *mut u8) -> Rd {
    let dst = (ctx.resume_ptr as *mut ResumeFrame).add(slot as usize);
    (*dst).base = target;
    (*dst).ti_ptr = core::ptr::null();
    (*dst).kind = FrameKind::AnyArrayBody;
    (*dst).state = 0;
    Rd::Suspend
}
