//! Resume stack: Go-allocated, outermost-first (reserve-slot pattern).
//!
//! rd_resume pops the innermost frame, dispatches to its body function.
//! If the body completes, continue to the next outer frame; if it
//! suspends, it reuses the freed slot — return immediately.

use crate::parser::{self, Rd};
use crate::typedesc::{DecArrayDesc, DecMapDesc, DecSliceDesc, DecStructDesc};
use crate::DecExecCtx;

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum FrameKind {
    Root,          // → rd_parse_value
    StructBody,    // → rd_body_to_struct
    SliceBody,     // → rd_body_to_slice
    StructEntry,   // → rd_parse_to_struct
    SliceEntry,    // → rd_parse_to_slice
    SliceInit,     // → rd_parse_to_slice_init
    MapEntry,      // → rd_parse_to_map
    MapInit,       // → rd_parse_to_map_init
    MapBody,       // → rd_body_to_map
    ArrayEntry,    // → rd_parse_to_array
    ArrayBody,     // → rd_body_to_array
    AnyObjectBody,  // → rd_parse_to_any_object
    AnyArrayBody,   // → rd_parse_to_any_array
    SkipObjectBody, // → rd_body_to_skip_object
    SkipArrayBody,  // → rd_body_to_skip_array
}

/// 32-byte frame. `state` interpretation depends on `kind`:
/// SliceBody: lo32=count, hi32=cap. ArrayBody/MapBody: lo32=count.
#[derive(Clone, Copy)]
#[repr(C)]
pub struct ResumeFrame {
    pub base: *mut u8,
    pub ti_ptr: *const u8,
    pub kind: FrameKind,
    pub state: u64,
}

impl ResumeFrame {
    #[inline]
    pub fn get_count_cap(&self) -> (u32, u32) {
        ((self.state & 0xFFFF_FFFF) as u32, (self.state >> 32) as u32)
    }

    #[inline]
    pub fn set_count_cap(&mut self, count: u32, cap: u32) {
        self.state = ((cap as u64) << 32) | (count as u64);
    }

    #[inline]
    pub fn get_count(&self) -> u32 {
        (self.state & 0xFFFF_FFFF) as u32
    }

    #[inline]
    pub fn set_count(&mut self, count: u32) {
        self.state = count as u64;
    }
}

const _: () = {
    let _ = [0u8; 32 - core::mem::size_of::<ResumeFrame>()];
    let _ = [0u8; core::mem::size_of::<ResumeFrame>() - 32];
};

/// Append a frame. Returns false if the stack is full.
#[inline]
pub unsafe fn push_frame(ctx: &mut DecExecCtx, frame: ResumeFrame) -> bool {
    let depth = ctx.resume_depth;
    if depth >= ctx.resume_cap {
        return false;
    }
    let dst = (ctx.resume_ptr as *mut ResumeFrame).add(depth as usize);
    (*dst).base = frame.base;
    (*dst).ti_ptr = frame.ti_ptr;
    (*dst).kind = frame.kind;
    (*dst).state = frame.state;
    ctx.resume_depth = depth + 1;
    true
}

/// Pop-and-dispatch: pop innermost frame, call its body function.
/// The body re-reserves the freed slot if it needs to suspend again.
pub unsafe fn rd_resume(ctx: &mut DecExecCtx) {
    let src = core::slice::from_raw_parts(ctx.src_ptr, ctx.src_len as usize);

    if ctx.resume_depth == 0 {
        ctx.exit_code = crate::exit::OK;
        return;
    }

    while ctx.resume_depth > 0 {
        let depth = ctx.resume_depth;
        let frame = &*(ctx.resume_ptr as *const ResumeFrame).add((depth - 1) as usize);
        ctx.resume_depth = depth - 1; // pop; body re-reserves if needed

        let result = match frame.kind {
            FrameKind::Root => {
                let kind = *(frame.ti_ptr);
                parser::rd_parse_value(ctx, src, kind, frame.ti_ptr, frame.base)
            }
            FrameKind::StructBody => parser::rd_body_to_struct(
                ctx,
                src,
                frame.base,
                frame.ti_ptr as *const DecStructDesc,
            ),
            FrameKind::StructEntry => parser::rd_parse_to_struct(
                ctx,
                src,
                frame.base,
                frame.ti_ptr as *const DecStructDesc,
            ),
            FrameKind::SliceBody => {
                parser::rd_body_to_slice(ctx, src, frame.base, frame.ti_ptr as *const DecSliceDesc)
            }
            FrameKind::SliceEntry => {
                parser::rd_parse_to_slice(ctx, src, frame.base, frame.ti_ptr as *const DecSliceDesc)
            }
            FrameKind::SliceInit => parser::rd_parse_to_slice_init(
                ctx,
                src,
                frame.base,
                frame.ti_ptr as *const DecSliceDesc,
            ),
            FrameKind::MapEntry => {
                parser::rd_parse_to_map(ctx, src, frame.base, frame.ti_ptr as *const DecMapDesc)
            }
            FrameKind::MapInit => parser::rd_parse_to_map_init(
                ctx,
                src,
                frame.base,
                frame.ti_ptr as *const DecMapDesc,
            ),
            FrameKind::MapBody => {
                parser::rd_body_to_map(ctx, src, frame.base, frame.ti_ptr as *const DecMapDesc)
            }
            FrameKind::ArrayEntry => {
                parser::rd_parse_to_array(ctx, src, frame.base, frame.ti_ptr as *const DecArrayDesc)
            }
            FrameKind::ArrayBody => {
                parser::rd_body_to_array(ctx, src, frame.base, frame.ti_ptr as *const DecArrayDesc)
            }
            FrameKind::AnyObjectBody => parser::rd_body_to_any_object(ctx, src, frame.base),
            FrameKind::AnyArrayBody => parser::rd_body_to_any_array(ctx, src, frame.base),
            FrameKind::SkipObjectBody => parser::rd_body_to_skip_object(ctx, src),
            FrameKind::SkipArrayBody => parser::rd_body_to_skip_array(ctx, src),
        };

        match result {
            Rd::Ok => continue,
            Rd::Suspend => {
                // Root: re-push if no child frame was pushed.
                if frame.kind == FrameKind::Root && ctx.resume_depth == depth - 1 {
                    push_frame(
                        ctx,
                        ResumeFrame {
                            base: frame.base,
                            ti_ptr: frame.ti_ptr,
                            kind: FrameKind::Root,
                            state: 0,
                        },
                    );
                }
                return;
            }
            Rd::Error => return,
        }
    }

    ctx.exit_code = crate::exit::OK;
}
