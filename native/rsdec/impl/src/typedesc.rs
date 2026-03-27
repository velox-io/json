//! Type descriptors compiled by Go, read by Rust parser.
//!
//! repr(C) structs memory-mapped from Go-allocated buffers.
//! Every descriptor starts with a `kind: u8` tag for dynamic dispatch.
//!
//! DecSliceDesc and DecMapDesc share identical layout:
//! kind → {inner}_kind → {inner}_has_ptr → {inner}_size →
//! {inner}_desc → codec_ptr → rtype.

/// Element type kinds — must match Go ElemTypeKind constants.
#[allow(dead_code)]
pub mod kind {
    pub const BOOL: u8 = 1;
    pub const INT: u8 = 2;
    pub const INT8: u8 = 3;
    pub const INT16: u8 = 4;
    pub const INT32: u8 = 5;
    pub const INT64: u8 = 6;
    pub const UINT: u8 = 7;
    pub const UINT8: u8 = 8;
    pub const UINT16: u8 = 9;
    pub const UINT32: u8 = 10;
    pub const UINT64: u8 = 11;
    pub const FLOAT32: u8 = 12;
    pub const FLOAT64: u8 = 13;
    pub const STRING: u8 = 14;
    pub const STRUCT: u8 = 15;
    pub const SLICE: u8 = 16;
    pub const ARRAY: u8 = 22;
    pub const MAP: u8 = 19;
    pub const POINTER: u8 = 17;
    pub const ANY: u8 = 18;

    #[inline]
    pub fn is_signed_int(k: u8) -> bool {
        k >= INT && k <= INT64
    }

    #[inline]
    pub fn is_unsigned_int(k: u8) -> bool {
        k >= UINT && k <= UINT64
    }

    #[inline]
    pub fn is_int(k: u8) -> bool {
        is_signed_int(k) || is_unsigned_int(k)
    }
}

/// DecStructDesc (16 + 24*N bytes): Go struct descriptor with inline fields.
#[repr(C)]
pub struct DecStructDesc {
    pub kind: u8,
    pub flags: u8,
    pub _pad: u16,
    pub size: u32,
    pub num_fields: u16,
    pub _pad2: u16,
    pub _pad3: u32,
}

impl DecStructDesc {
    #[inline]
    pub unsafe fn fields(&self) -> &[DecFieldDesc] {
        let base = (self as *const Self).add(1) as *const DecFieldDesc;
        core::slice::from_raw_parts(base, self.num_fields as usize)
    }

    /// Linear scan field lookup (~40ns for ≤8 fields, competitive with hash).
    #[inline]
    pub unsafe fn find_field(&self, name: &[u8]) -> Option<&DecFieldDesc> {
        let fields = self.fields();
        for f in fields {
            let fname = core::slice::from_raw_parts(f.name_ptr, f.name_len as usize);
            if bytes_equal(fname, name) {
                return Some(f);
            }
        }
        None
    }

    #[inline]
    pub unsafe fn field_at(&self, idx: u16) -> &DecFieldDesc {
        let base = (self as *const Self).add(1) as *const DecFieldDesc;
        &*base.add(idx as usize)
    }
}

/// DecFieldDesc (24 bytes): one struct field.
#[repr(C)]
pub struct DecFieldDesc {
    pub name_ptr: *const u8,
    pub name_len: u16,
    pub offset: u16,
    pub val_kind: u8,
    pub val_flags: u8,
    pub _pad: u16,
    pub val_desc: *const u8,
}

/// DecSliceDesc (32 bytes): Go []T descriptor.
#[repr(C)]
pub struct DecSliceDesc {
    pub kind: u8,
    pub elem_kind: u8,
    pub elem_has_ptr: u8,
    pub _pad: u8,
    pub elem_size: u32,
    pub elem_desc: *const u8,
    pub codec_ptr: *const u8,
    pub elem_rtype: *const u8,
}

/// DecMapDesc (32 bytes): Go map[string]V descriptor.
#[repr(C)]
pub struct DecMapDesc {
    pub kind: u8,
    pub val_kind: u8,
    pub val_has_ptr: u8,
    pub _pad: u8,
    pub val_size: u32,
    pub val_desc: *const u8,
    pub codec_ptr: *const u8,
    pub map_rtype: *const u8,
}

/// DecArrayDesc (24 bytes): Go [N]T descriptor.
/// Memory is inline in the parent struct; no allocation needed.
#[repr(C)]
pub struct DecArrayDesc {
    pub kind: u8,
    pub elem_kind: u8,
    pub elem_has_ptr: u8,
    pub _pad: u8,
    pub elem_size: u32,
    pub elem_desc: *const u8,
    pub array_len: u32,
    pub _pad2: u32,
}

/// DecPointerDesc (24 bytes): Go *T descriptor.
/// Pointer target is allocated by Go on demand (yield).
#[repr(C)]
pub struct DecPointerDesc {
    pub kind: u8,
    pub elem_kind: u8,
    pub elem_has_ptr: u8,
    pub _pad: u8,
    pub elem_size: u32,
    pub elem_desc: *const u8,
    pub elem_rtype: *const u8,
}

// Compile-time size checks
const _: () = {
    // DecStructDesc header is 16 bytes
    let _ = [0u8; 16 - core::mem::size_of::<DecStructDesc>()];
    let _ = [0u8; core::mem::size_of::<DecStructDesc>() - 16];
    // DecFieldDesc is 24 bytes
    let _ = [0u8; 24 - core::mem::size_of::<DecFieldDesc>()];
    let _ = [0u8; core::mem::size_of::<DecFieldDesc>() - 24];
    // DecSliceDesc is 32 bytes
    let _ = [0u8; 32 - core::mem::size_of::<DecSliceDesc>()];
    let _ = [0u8; core::mem::size_of::<DecSliceDesc>() - 32];
    // DecMapDesc is 32 bytes
    let _ = [0u8; 32 - core::mem::size_of::<DecMapDesc>()];
    let _ = [0u8; core::mem::size_of::<DecMapDesc>() - 32];
    // DecArrayDesc is 24 bytes
    let _ = [0u8; 24 - core::mem::size_of::<DecArrayDesc>()];
    let _ = [0u8; core::mem::size_of::<DecArrayDesc>() - 24];
    // DecPointerDesc is 24 bytes
    let _ = [0u8; 24 - core::mem::size_of::<DecPointerDesc>()];
    let _ = [0u8; core::mem::size_of::<DecPointerDesc>() - 24];
};

/// Compare byte slices without memcmp (avoids external linkage in no_std).
#[inline]
pub fn bytes_equal(a: &[u8], b: &[u8]) -> bool {
    if a.len() != b.len() {
        return false;
    }
    let len = a.len();
    let mut i = 0;
    while i < len {
        if a[i] != b[i] {
            return false;
        }
        i += 1;
    }
    true
}
