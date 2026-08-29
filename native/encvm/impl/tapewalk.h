/*
 * value.Value tape walk: re-serialize a tape-backed parsed value straight
 * into the encoder output buffer.
 *
 * The walk is a single linear scan over the tape words of one Value, driven
 * by a VjStackFrame so a window-full exit resumes at the element boundary
 * where it stopped. Comma placement needs no depth stack: a single
 * pending_comma bit, cleared on container open and set by any element or
 * container close, reproduces the recursive serializer's output. Container
 * kinds (array vs object) live in a two-bits-per-container kindstack: the
 * low pair names the current container, and object element-start strings
 * are member keys.
 *
 * The walk bound is the root word's paired close index (plus one in full
 * mode): the loop exits when the cursor passes it, so a kindstack whose
 * bits shift out past 16 open containers cannot truncate output. Compact
 * builds (VJ_COMPACT_INDENT) exploit that by carrying no depth state at
 * all: the prefix is a bare comma and the guard scan is compiled out, so
 * nesting depth is unbounded. Indent builds keep vj_tape_max_depth: the
 * per-container indent depth is the kindstack popcount and the indent
 * template is finite.
 *
 * Loop state (cursor, kindstack, flags) lives in locals and is committed
 * to the frame only at the exits, so a window-full retry never re-emits an
 * element. The ctx fields are hoisted into locals once: the cold emit
 * helper writes through uint8_t pointers, which under TBAA may alias the
 * ctx fields and would otherwise force per-word reloads.
 *
 * Dispatch is threaded: every handler ends by fetching the next word and
 * jumping through a 256-slot table keyed on the tag byte, so each
 * handler owns an indirect branch site and the BTB learns its successors
 * independently. Slots hold int32 byte offsets from a base label, and the
 * base is materialized by an adr/lea immediate: a raw label address
 * stored in data would land in an unnamed constant pool whose relocation
 * prelink does not carry (the VM dispatch_table contract). Unlisted tags
 * carry offset 0, the base label's own handler (the unknown-tag skip).
 *
 * Escape policy is the mandatory set only: a Value is pre-parsed JSON and
 * its MarshalJSON output is never re-escaped by stdlib, so the optional
 * modes (HTML, line terms, invalid UTF-8) do not apply. Doubles format
 * through the encoder's own float writer so a Value number and a float64
 * field render identically.
 */

#ifndef VJ_ENCVM_TAPEWALK_H
#define VJ_ENCVM_TAPEWALK_H

#include <stddef.h>
#include <stdint.h>

#include "ftoa.h"
#include "itoa.h"
#include "macros.h"
#include "strfn.h"
#include "swissmap.h"
#include "types.h"

/* Go value.Value / internal/valueabi.Doc ABI mirrors. */

typedef struct GoValueDoc {
  GoSlice tape;      /*  0: []uint64, whole-parse tape */
  GoSlice str_arena; /* 24: []byte, backs string / numraw / double text */
  GoSlice src;       /* 48: []byte, original JSON source (StrRaw bodies) */
  uint8_t zero_copy; /* 72 */
  uint8_t _pad[7];   /* 73 */
} GoValueDoc;

_Static_assert(sizeof(GoValueDoc) == 80, "GoValueDoc must mirror internal/valueabi.Doc");
_Static_assert(offsetof(GoValueDoc, tape) == 0, "Doc.tape offset");
_Static_assert(offsetof(GoValueDoc, str_arena) == 24, "Doc.str_arena offset");
_Static_assert(offsetof(GoValueDoc, src) == 48, "Doc.src offset");
_Static_assert(offsetof(GoValueDoc, zero_copy) == 72, "Doc.zero_copy offset");

typedef struct GoValue {
  const GoValueDoc *doc; /*  0 */
  int32_t base;          /*  8: absolute tape word offset of this Value's region */
  int32_t tidx;          /* 12: cursor relative to base */
  int32_t end;           /* 16 */
  int32_t mode;          /* 20: packed view mode: seam shift in the low bits, flags above */
} GoValue;

_Static_assert(sizeof(GoValue) == 24, "GoValue must mirror value.Value");
_Static_assert(offsetof(GoValue, doc) == 0, "Value.doc offset");
_Static_assert(offsetof(GoValue, base) == 8, "Value.base offset");
_Static_assert(offsetof(GoValue, tidx) == 12, "Value.tidx offset");
_Static_assert(offsetof(GoValue, mode) == 20, "Value.mode offset");

/* Tape word tags. Mirror of value/value.go and ndec core/tape.h (the
 * authoritative definitions);
 **/

#define VJ_TAPE_PAYLOAD_MASK 0x00FFFFFFFFFFFFFFULL

#define VJ_TARR_BEG ((uint64_t)'[' << 56) /* low32 close idx, bits 32..55 count */
#define VJ_TARR_END ((uint64_t)']' << 56)
#define VJ_TOBJ_BEG ((uint64_t)'{' << 56)
#define VJ_TOBJ_END ((uint64_t)'}' << 56)
#define VJ_TSTRING  ((uint64_t)'"' << 56) /* payload: arena off(0..31) | len(32..55) */
#define VJ_TSTR_RAW ((uint64_t)'R' << 56) /* payload: src off(0..31) | len(32..55) */
/* VJ_TSTR_FREE is VJ_TSTR_RAW's predicate with arena backing: the body is
 * backslash-free source text, so verbatim emission reproduces it. */
#define VJ_TSTR_FREE ((uint64_t)'S' << 56) /* payload: arena off(0..31) | len(32..55) */
#define VJ_TINT64    ((uint64_t)'l' << 56) /* value word follows */
#define VJ_TUINT64   ((uint64_t)'u' << 56) /* value word follows */
#define VJ_TDOUBLE   ((uint64_t)'d' << 56) /* value word follows */
#define VJ_TNUM_RAW  ((uint64_t)'D' << 56) /* one word: arena off | len */
#define VJ_TTRUE     ((uint64_t)'t' << 56)
#define VJ_TFALSE    ((uint64_t)'f' << 56)
#define VJ_TNULL     ((uint64_t)'n' << 56)

#define VJ_TSEAM_MASK 0x7FFFFFFFu

/* View-mode layout mirrors ndec's TAPE_VIEW_SHIFT_MASK: the low bits of
 * GoValue.mode name the seam view, the bits above are mode flags the walker
 * must strip before shifting. */
#define VJ_TVIEW_SHIFT_MASK 0x1Fu

/* Walk frame flags (f->walk.flags). */
#define VJ_WALK_PENDING_COMMA 0x01u /* a comma is due before the next element */
#define VJ_WALK_AFTER_KEY     0x02u /* next word is a member value: no comma */
#define VJ_WALK_SPREAD        0x08u /* member mode: no outer braces */
#define VJ_WALK_HOST_FIRST    0x10u /* spread: host first latch was set at entry */
#define VJ_WALK_WROTE_ANY     0x20u /* spread: at least one member emitted */

typedef struct VjTapeWalkCtx {
  const uint64_t *tape;     /* doc->tape.data */
  const uint8_t *str_arena; /* doc->str_arena.data */
  const uint8_t *src;       /* doc->src.data */
  int32_t base;             /* value base */
  uint32_t shift;           /* seam view shift */
  uint32_t flags;           /* escape flags (mandatory-only) + float mode */
} VjTapeWalkCtx;

enum {
  VJ_TAPE_WALK_BUF_FULL = 1,
  VJ_TAPE_WALK_DONE     = 2,
};

#ifdef VJ_COMPACT_INDENT
/* Compact builds carry no indent state; the helpers below are indent-only. */
#define vj_tw_pad(ind, depth)               ((int)0)
#define vj_tw_write_indent(buf, ind, depth) (buf)
#else
INLINE int vj_tw_pad(const VjSwissIndent *ind, int32_t depth) {
  return ind->indent_step ? (1 + (int)ind->indent_prefix_len + depth * (int)ind->indent_step) : 0;
}

INLINE uint8_t *vj_tw_write_indent(uint8_t *buf, const VjSwissIndent *ind, int32_t depth) {
  if (ind->indent_step) {
    int n = 1 + (int)ind->indent_prefix_len + depth * (int)ind->indent_step;
    __builtin_memcpy(buf, ind->indent_tpl, (size_t)n);
    buf += n;
  }
  return buf;
}
#endif

/* Verbatim body copy: fixed-width SIMD chunks in the escape writer's
 * escape-free shape. A variable-length memcpy lowers to a libc call, which
 * is slower than the inline NEON tail; every string word now takes this
 * path, so it must stay call-free. The 16-byte tail store overlaps past
 * the body by the same amount the escape writer's tail does; the window
 * contract covers it identically. */
INLINE uint32_t vj_tw_copy_verbatim(uint8_t *buf, const uint8_t *sp, uint32_t len) {
  uint32_t i = 0;
  while (i + 16 <= len) {
    vj_store16(buf + i, vj_load16(sp + i));
    i += 16;
  }
  if (len > i) {
    vj_v16u8 v = vj_load16(sp + i);
    vj_store16(buf + i, v);
  }
  return len;
}

/* Skip seam words following the Value's view. The zero-distance floor is a
 * liveness guard: a hang is the one failure a caller cannot contain. */
INLINE int32_t vj_tw_skip_seams(const VjTapeWalkCtx *c, int32_t idx) {
  uint64_t w = c->tape[c->base + idx];
  while ((int64_t)w < 0) {
    uint32_t d = (uint32_t)((w >> c->shift) & VJ_TSEAM_MASK);
    if (d == 0) d = 1;
    idx += (int32_t)d;
    w = c->tape[c->base + idx];
  }
  return idx;
}

/* Same with hoisted tape/base locals: the walk loop holds them in
 * registers across the cold emit helper's uint8_t writes. */
INLINE int32_t vj_tw_skip_seams_h(const uint64_t *tape, int32_t base, uint32_t shift, int32_t idx) {
  uint64_t w = tape[base + idx];
  while ((int64_t)w < 0) {
    uint32_t d = (uint32_t)((w >> shift) & VJ_TSEAM_MASK);
    if (d == 0) d = 1;
    idx += (int32_t)d;
    w = tape[base + idx];
  }
  return idx;
}

#ifndef VJ_COMPACT_INDENT
/* Max container nesting within the Value, counted over the words its view
 * exposes. Root scalars report 0. Bounds the walk's kindstack depth and
 * the indent template levels the walk will touch. Compact builds need
 * neither: the kindstack overflow is benign there (cursor-bounded exit)
 * and no indent exists. */
NOINLINE static int32_t vj_tape_max_depth(const GoValue *v) {
  const uint64_t *tape = (const uint64_t *)v->doc->tape.data;
  const int32_t b      = v->base;
  uint64_t w           = tape[b + v->tidx];
  switch (w & 0xFF00000000000000ULL) {
  case VJ_TARR_BEG:
  case VJ_TOBJ_BEG:
    break;
  default:
    return 0;
  }
  int32_t end   = (int32_t)(w & 0xFFFFFFFFu) + 1;
  int32_t cur   = v->tidx;
  int32_t depth = 0, maxd = 0;
  while (cur < end) {
    w = tape[b + cur];
    if ((int64_t)w < 0) {
      uint32_t d = (uint32_t)((w >> ((uint32_t)v->mode & VJ_TVIEW_SHIFT_MASK)) & VJ_TSEAM_MASK);
      if (d == 0) d = 1;
      cur += (int32_t)d;
      continue;
    }
    switch (w & 0xFF00000000000000ULL) {
    case VJ_TARR_BEG:
    case VJ_TOBJ_BEG:
      depth++;
      if (depth > maxd) maxd = depth;
      cur++;
      break;
    case VJ_TARR_END:
    case VJ_TOBJ_END:
      depth--;
      cur++;
      break;
    case VJ_TINT64:
    case VJ_TUINT64:
    case VJ_TDOUBLE:
      cur += 2;
      break;
    default:
      cur++;
      break;
    }
  }
  return maxd;
}
#endif /* !VJ_COMPACT_INDENT */

/* NOINLINE wrapper: vj_write_float64 is INLINE and its formatting machinery
 * would otherwise dominate the walk step's frame. */
NOINLINE static int vj_tw_write_double(uint8_t *buf, double dval, uint32_t flags) {
  return vj_write_float64(buf, dval, (flags & VJ_FLAGS_FLOAT_EXP_AUTO) ? VJ_FTOA_EXP_AUTO : VJ_FTOA_FIXED);
}

/* Mandatory-set prescan leaf: SWAR scan, general-purpose registers only.
 * Cold path (the pessimistic 6x estimate overflowed the window), so the
 * scalar scan costs nothing on the hot path, and staying out of vector
 * registers keeps the win64 frame at the ABI minimum (a vector scan pays
 * the xmm6+ save block). Returns the same tight upper bound format as
 * vj_prescan_string_escaped_len with zero escape flags. */
NOINLINE static int64_t vj_tw_prescan_escaped_len(const uint8_t *sp, uint32_t len) {
  int64_t esc = 0;
  int64_t i   = 0;
  /* Scalar by request: auto-vectorization brings the xmm6+ save block
   * back into the prologue, tail loop included. */
#pragma clang loop vectorize(disable) interleave(disable)
  while (i + 8 <= (int64_t)len) {
    uint64_t w;
    __builtin_memcpy(&w, sp + i, 8);
    esc += __builtin_popcount((uint32_t)vj_escape_mask_8_fast(w));
    i += 8;
  }
#pragma clang loop vectorize(disable) interleave(disable)
  for (; i < (int64_t)len; i++) {
    uint8_t ch = sp[i];
    esc += (ch < 0x20 || ch == '"' || ch == '\\');
  }
  return 2 + (int64_t)len + esc * 5;
}

/* Cold-string emit: the window could not hold the pessimistic 6x bound.
 * The caller's overhead must already include the room it still needs
 * after the element (a member key's ':' plus the indent-mode space): the
 * suffix is part of the element's footprint, so the window checks must
 * cover it. Returns the advanced buffer, or NULL when the window cannot
 * hold the element even with the tight prescan bound. NOINLINE so the
 * escape and prescan spills stay out of the walk step's frame.
 *
 * The escape is the fast (mandatory-set) writer: the walk's escape flags
 * are structurally zero (the VM masks the optional modes out of the walk
 * ctx), and its output is byte-identical to the full escape with zero
 * flags: control characters, quote and backslash escaped, every other
 * byte (non-ASCII included) verbatim. */
NOINLINE static uint8_t *vj_tw_emit_string(uint8_t *buf, const uint8_t *bend, const uint8_t *sp, uint32_t len,
                                           int verbatim, int64_t overhead, int comma, const VjSwissIndent *ind,
                                           int32_t ind_depth) {
  if (verbatim) {
    /* Verbatim bodies are plain source text: StrRaw spans alias src,
     * StrFree copies sit in the arena. Escape-free by the producer
     * invariant. */
    if (buf + overhead + 2 + (int64_t)len > bend) return NULL;
    if (comma) *buf++ = ',';
#ifndef VJ_COMPACT_INDENT
    if (ind_depth >= 0) buf = vj_tw_write_indent(buf, ind, ind_depth);
#endif
    *buf++ = '"';
    __builtin_memcpy(buf, sp, len);
    buf += len;
    *buf++ = '"';
    return buf;
  }
  /* The escape dispatch writes the surrounding quotes itself. */
  int64_t need = overhead + 2 + (int64_t)len * 6;
  if (UNLIKELY(buf + need > bend)) {
    int64_t tight = overhead + vj_tw_prescan_escaped_len(sp, len);
    if (buf + tight > bend) return NULL;
  }
  if (comma) *buf++ = ',';
#ifndef VJ_COMPACT_INDENT
  if (ind_depth >= 0) buf = vj_tw_write_indent(buf, ind, ind_depth);
#endif
  buf += VJ_ESCAPE_STRING_FAST_DISPATCH(buf, sp, len);
  return buf;
}

/* Advance the walk word by word until the value is fully emitted
 * (VJ_TAPE_WALK_DONE) or an element does not fit the window
 * (VJ_TAPE_WALK_BUF_FULL, frame left at that element's start so the op
 * re-executes cleanly after a grow). Frame state mutates only at the
 * exits, after the element's bytes are committed, so a retry never
 * duplicates output. */
NOINLINE static int vj_tape_walk_step(uint8_t **bufp, const uint8_t *bend, VjStackFrame *f, const VjTapeWalkCtx *c,
                                      const VjSwissIndent *ind) {
  /* Threaded-dispatch table over the tag byte; see the header comment. */
#define VJ_TW_ENTRY(label) (int32_t)((char *) && label - (char *) && vj_tw_dispatch_base)
  ALIGNED_DECL(64)
  static const int32_t tw_dispatch[256] ALIGNED(64) = {
      ['['] = VJ_TW_ENTRY(L_open),   ['{'] = VJ_TW_ENTRY(L_open),   [']'] = VJ_TW_ENTRY(L_close),
      ['}'] = VJ_TW_ENTRY(L_close),  ['"'] = VJ_TW_ENTRY(L_string), ['R'] = VJ_TW_ENTRY(L_string),
      ['S'] = VJ_TW_ENTRY(L_string), ['l'] = VJ_TW_ENTRY(L_int),    ['u'] = VJ_TW_ENTRY(L_uint),
      ['d'] = VJ_TW_ENTRY(L_double), ['D'] = VJ_TW_ENTRY(L_numraw), ['t'] = VJ_TW_ENTRY(L_true),
      ['f'] = VJ_TW_ENTRY(L_false),  ['n'] = VJ_TW_ENTRY(L_null),
  };
#undef VJ_TW_ENTRY

  uint8_t *buf     = *bufp;
  const GoValue *v = (const GoValue *)f->walk.value;

  const int spread = (f->walk.flags & VJ_WALK_SPREAD) != 0;
  int32_t cur      = f->walk.cursor;
  uint32_t kinds   = f->walk.kindstack;
  uint32_t flags   = f->walk.flags;

  /* Hoisted once: the cold emit helper writes through uint8_t pointers,
   * which under TBAA may alias these ctx fields. */
  const uint64_t *tape = c->tape;
  const int32_t base   = c->base;
  const uint32_t shift = c->shift;
  const uint8_t *arena = c->str_arena;
  const uint8_t *src   = c->src;

  /* Walk bound and root word. A container root's open word carries its
   * close index; full mode emits the close word itself, spread stops
   * before it. A scalar root spans one or two words. */
  const int32_t root_idx = vj_tw_skip_seams_h(tape, base, shift, v->tidx);
  const uint64_t rootw   = tape[base + root_idx];
  int32_t endb;
  switch (rootw & 0xFF00000000000000ULL) {
  case VJ_TARR_BEG:
  case VJ_TOBJ_BEG:
    endb = (int32_t)(rootw & 0xFFFFFFFFu);
    if (!spread) endb++;
    break;
  case VJ_TINT64:
  case VJ_TUINT64:
  case VJ_TDOUBLE:
    endb = root_idx + 2;
    break;
  default:
    endb = root_idx + 1;
    break;
  }

#ifndef VJ_COMPACT_INDENT
  const int32_t depth_adj = (int32_t)ind->indent_depth - (spread ? 1 : 0);
#endif

/* Tag dispatch via base+offset: the adr/lea immediate form keeps the
 * base label out of unnamed constant pools, so prelink carries no .quad
 * relocation for it (the VM_DISPATCH contract). */
#if defined(__aarch64__)
#define VJ_TW_DISPATCH(tb)                                                                                        \
  do {                                                                                                            \
    char *_base;                                                                                                  \
    __asm__ volatile("adr %0, %c1" : "=r"(_base) : "i"(&&vj_tw_dispatch_base));                                   \
    char *_tgt = _base + tw_dispatch[tb];                                                                         \
    __asm__ volatile("" : "+r"(_tgt));                                                                            \
    goto *(void *)_tgt;                                                                                           \
  } while (0)
#elif defined(__x86_64__)
#define VJ_TW_DISPATCH(tb)                                                                                        \
  do {                                                                                                            \
    char *_base;                                                                                                  \
    __asm__ volatile("lea %c1(%%rip), %0" : "=r"(_base) : "i"(&&vj_tw_dispatch_base));                            \
    char *_tgt = _base + tw_dispatch[tb];                                                                         \
    __asm__ volatile("" : "+r"(_tgt));                                                                            \
    goto *(void *)_tgt;                                                                                           \
  } while (0)
#else
#error "VJ_TW_DISPATCH: unsupported architecture (need aarch64 or x86_64)"
#endif

/* Fetch the next word and thread to its handler. The bound precedes the
 * seam skip: past the value's last word the tape may end, and skip would
 * read one word out of range. */
#define VJ_TW_NEXT()                                                                                              \
  do {                                                                                                            \
    if (UNLIKELY(cur >= endb)) goto done;                                                                         \
    cur = vj_tw_skip_seams_h(tape, base, shift, cur);                                                             \
    if (UNLIKELY(cur >= endb)) goto done;                                                                         \
    w = tape[base + cur];                                                                                         \
    VJ_TW_DISPATCH((uint8_t)(w >> 56));                                                                           \
  } while (0)

#ifdef VJ_COMPACT_INDENT
/* Element prefix: comma before the token. The root position (full mode,
 * first word) owns nothing: the host wrote the key and its indent. A
 * spread's first member follows the host first latch: the host object
 * open already indented when set. */
#define VJ_TW_PREFIX()                                                                                            \
  int comma           = 0;                                                                                        \
  const int after_key = (flags & VJ_WALK_AFTER_KEY) != 0;                                                         \
  do {                                                                                                            \
    if (!after_key) {                                                                                             \
      if (spread) {                                                                                               \
        if (!(flags & VJ_WALK_WROTE_ANY) && !(flags & VJ_WALK_HOST_FIRST)) comma = 1;                             \
        else if (flags & VJ_WALK_PENDING_COMMA)                                                                   \
          comma = 1;                                                                                              \
      } else if (cur != root_idx) {                                                                               \
        if (flags & VJ_WALK_PENDING_COMMA) comma = 1;                                                             \
      }                                                                                                           \
    }                                                                                                             \
  } while (0);                                                                                                    \
  const int64_t overhead  = comma;                                                                                \
  const int32_t ind_depth = -1
#else
/* Same prefix with indent: depth = open-container count = marker bits
 * (one per 2-bit group). */
#define VJ_TW_PREFIX()                                                                                            \
  int comma           = 0;                                                                                        \
  const int after_key = (flags & VJ_WALK_AFTER_KEY) != 0;                                                         \
  int32_t ind_depth   = -1;                                                                                       \
  do {                                                                                                            \
    if (!after_key) {                                                                                             \
      int32_t depth = depth_adj + __builtin_popcount(kinds & 0xAAAAAAAAu);                                        \
      if (!spread && cur == root_idx) {                                                                           \
        /* root position: no prefix */                                                                            \
      } else if (spread && !(flags & VJ_WALK_WROTE_ANY)) {                                                        \
        if (!(flags & VJ_WALK_HOST_FIRST)) {                                                                      \
          comma     = 1;                                                                                          \
          ind_depth = depth;                                                                                      \
        }                                                                                                         \
      } else if (flags & VJ_WALK_PENDING_COMMA) {                                                                 \
        comma     = 1;                                                                                            \
        ind_depth = depth;                                                                                        \
      } else {                                                                                                    \
        ind_depth = depth;                                                                                        \
      }                                                                                                           \
    }                                                                                                             \
  } while (0);                                                                                                    \
  const int64_t overhead = comma + (ind_depth >= 0 ? vj_tw_pad(ind, ind_depth) : 0)
#endif

  uint64_t w;
  if (cur >= endb) goto done;
  cur = vj_tw_skip_seams_h(tape, base, shift, cur);
  if (cur >= endb) goto done;
  w = tape[base + cur];
  VJ_TW_DISPATCH((uint8_t)(w >> 56));

L_open: {
  const uint8_t tb = (uint8_t)(w >> 56);
  VJ_TW_PREFIX();
  if (buf + overhead + 1 > bend) goto buf_full;
  if (comma) *buf++ = ',';
#ifndef VJ_COMPACT_INDENT
  if (ind_depth >= 0) buf = vj_tw_write_indent(buf, ind, ind_depth);
#endif
  *buf++ = (tb == '{') ? '{' : '[';
  /* Push the container kind: two bits per container (a marker bit plus
   * the kind), so every push sets exactly one bit. The popcount is the
   * open-container depth, the low pair names the current container
   * (0b10 array, 0b11 object), and a pop is a plain right shift. Bits
   * shifted out past 16 containers are dead state: the exit is cursor
   * bounded and only the low pair is live. */
  kinds = (kinds << 2) | 0b10 | (uint32_t)(tb == '{');
  cur++;
  flags = (flags & ~(uint32_t)(VJ_WALK_PENDING_COMMA | VJ_WALK_AFTER_KEY)) | VJ_WALK_WROTE_ANY;
  VJ_TW_NEXT();
}

L_close: {
  /* pending==0 inside this container means no element was written. */
  const int had_elems = (flags & VJ_WALK_PENDING_COMMA) != 0;
#ifdef VJ_COMPACT_INDENT
  if (buf + 1 > bend) goto buf_full;
#else
  int64_t need = 1 + (had_elems ? vj_tw_pad(ind, depth_adj + __builtin_popcount(kinds & 0xAAAAAAAAu) - 1) : 0);
  if (buf + need > bend) goto buf_full;
  if (had_elems) buf = vj_tw_write_indent(buf, ind, depth_adj + __builtin_popcount(kinds & 0xAAAAAAAAu) - 1);
#endif
  *buf++ = (uint8_t)(w >> 56);
  kinds >>= 2;
  cur++;
  flags = (flags & ~(uint32_t)VJ_WALK_AFTER_KEY) | VJ_WALK_PENDING_COMMA | VJ_WALK_WROTE_ANY;
  VJ_TW_NEXT();
}

L_true: {
  VJ_TW_PREFIX();
  if (buf + overhead + 4 > bend) goto buf_full;
  if (comma) *buf++ = ',';
#ifndef VJ_COMPACT_INDENT
  if (ind_depth >= 0) buf = vj_tw_write_indent(buf, ind, ind_depth);
#endif
  __builtin_memcpy(buf, "true", 4);
  buf += 4;
  cur++;
  flags = (flags & ~(uint32_t)VJ_WALK_AFTER_KEY) | VJ_WALK_PENDING_COMMA | VJ_WALK_WROTE_ANY;
  VJ_TW_NEXT();
}

L_false: {
  VJ_TW_PREFIX();
  if (buf + overhead + 5 > bend) goto buf_full;
  if (comma) *buf++ = ',';
#ifndef VJ_COMPACT_INDENT
  if (ind_depth >= 0) buf = vj_tw_write_indent(buf, ind, ind_depth);
#endif
  __builtin_memcpy(buf, "false", 5);
  buf += 5;
  cur++;
  flags = (flags & ~(uint32_t)VJ_WALK_AFTER_KEY) | VJ_WALK_PENDING_COMMA | VJ_WALK_WROTE_ANY;
  VJ_TW_NEXT();
}

L_null: {
  VJ_TW_PREFIX();
  if (buf + overhead + 4 > bend) goto buf_full;
  if (comma) *buf++ = ',';
#ifndef VJ_COMPACT_INDENT
  if (ind_depth >= 0) buf = vj_tw_write_indent(buf, ind, ind_depth);
#endif
  __builtin_memcpy(buf, "null", 4);
  buf += 4;
  cur++;
  flags = (flags & ~(uint32_t)VJ_WALK_AFTER_KEY) | VJ_WALK_PENDING_COMMA | VJ_WALK_WROTE_ANY;
  VJ_TW_NEXT();
}

L_int: {
  VJ_TW_PREFIX();
  if (buf + overhead + 21 > bend) goto buf_full;
  if (comma) *buf++ = ',';
#ifndef VJ_COMPACT_INDENT
  if (ind_depth >= 0) buf = vj_tw_write_indent(buf, ind, ind_depth);
#endif
  buf += write_int64(buf, (int64_t)tape[base + cur + 1]);
  cur += 2;
  flags = (flags & ~(uint32_t)VJ_WALK_AFTER_KEY) | VJ_WALK_PENDING_COMMA | VJ_WALK_WROTE_ANY;
  VJ_TW_NEXT();
}

L_uint: {
  VJ_TW_PREFIX();
  if (buf + overhead + 21 > bend) goto buf_full;
  if (comma) *buf++ = ',';
#ifndef VJ_COMPACT_INDENT
  if (ind_depth >= 0) buf = vj_tw_write_indent(buf, ind, ind_depth);
#endif
  buf += write_uint64(buf, tape[base + cur + 1]);
  cur += 2;
  flags = (flags & ~(uint32_t)VJ_WALK_AFTER_KEY) | VJ_WALK_PENDING_COMMA | VJ_WALK_WROTE_ANY;
  VJ_TW_NEXT();
}

L_double: {
  VJ_TW_PREFIX();
  /* Tape doubles come from parsed JSON, so they are always finite. */
  if (buf + overhead + 32 > bend) goto buf_full;
  if (comma) *buf++ = ',';
#ifndef VJ_COMPACT_INDENT
  if (ind_depth >= 0) buf = vj_tw_write_indent(buf, ind, ind_depth);
#endif
  double dval;
  __builtin_memcpy(&dval, &tape[base + cur + 1], 8);
  buf += vj_tw_write_double(buf, dval, c->flags);
  cur += 2;
  flags = (flags & ~(uint32_t)VJ_WALK_AFTER_KEY) | VJ_WALK_PENDING_COMMA | VJ_WALK_WROTE_ANY;
  VJ_TW_NEXT();
}

L_numraw: {
  uint64_t payload  = w & VJ_TAPE_PAYLOAD_MASK;
  uint32_t off      = (uint32_t)(payload & 0xFFFFFFFFu);
  uint32_t len      = (uint32_t)((payload >> 32) & 0xFFFFFF);
  const uint8_t *sp = arena + off;
  VJ_TW_PREFIX();
  if (buf + overhead + (int64_t)len > bend) goto buf_full;
  if (comma) *buf++ = ',';
#ifndef VJ_COMPACT_INDENT
  if (ind_depth >= 0) buf = vj_tw_write_indent(buf, ind, ind_depth);
#endif
  __builtin_memcpy(buf, sp, len);
  buf += len;
  cur++;
  flags = (flags & ~(uint32_t)VJ_WALK_AFTER_KEY) | VJ_WALK_PENDING_COMMA | VJ_WALK_WROTE_ANY;
  VJ_TW_NEXT();
}

L_string: {
  const uint8_t tb  = (uint8_t)(w >> 56);
  uint64_t payload  = w & VJ_TAPE_PAYLOAD_MASK;
  uint32_t off      = (uint32_t)(payload & 0xFFFFFFFFu);
  uint32_t len      = (uint32_t)((payload >> 32) & 0xFFFFFF);
  int verbatim      = (tb != '"');
  const uint8_t *sp = (tb == 'R') ? (src + off) : (arena + off);

  VJ_TW_PREFIX();

  /* An element-start string inside an object is a member key: the low
   * kindstack pair of the current container is 0b11. The key suffix
   * (':' plus the indent-mode space) is written by this handler right
   * after the element, so its bytes join the element's footprint. */
  const int is_key = (!after_key && (kinds & 1)) != 0;
#ifdef VJ_COMPACT_INDENT
  const int64_t key_suffix = is_key;
#else
  const int64_t key_suffix = is_key ? (ind->indent_step ? 2 : 1) : 0;
#endif

  if (verbatim) {
    /* Verbatim bodies: StrRaw src spans and StrFree arena copies are
     * escape-free source text: tight bound. */
    if (LIKELY(buf + overhead + key_suffix + 2 + (int64_t)len <= bend)) {
      if (comma) *buf++ = ',';
#ifndef VJ_COMPACT_INDENT
      if (ind_depth >= 0) buf = vj_tw_write_indent(buf, ind, ind_depth);
#endif
      *buf++ = '"';
      buf += vj_tw_copy_verbatim(buf, sp, len);
      *buf++ = '"';
    } else {
      uint8_t *nb = vj_tw_emit_string(buf, bend, sp, len, verbatim, overhead + key_suffix, comma, ind, ind_depth);
      if (UNLIKELY(nb == NULL)) goto buf_full;
      buf = nb;
    }
  } else if (LIKELY(buf + overhead + key_suffix + 2 + (int64_t)len * 6 <= bend)) {
    /* Inline emission: the pessimistic bound covers the worst-case
     * escape expansion, so the write needs no re-check. */
    if (comma) *buf++ = ',';
#ifndef VJ_COMPACT_INDENT
    if (ind_depth >= 0) buf = vj_tw_write_indent(buf, ind, ind_depth);
#endif
    buf += vj_escape_string_fast_inline(buf, sp, len);
  } else {
    uint8_t *nb = vj_tw_emit_string(buf, bend, sp, len, verbatim, overhead + key_suffix, comma, ind, ind_depth);
    if (UNLIKELY(nb == NULL)) goto buf_full;
    buf = nb;
  }
  if (is_key) {
    *buf++ = ':';
#ifndef VJ_COMPACT_INDENT
    if (ind->indent_step) *buf++ = ' ';
#endif
    cur++;
    uint32_t wflags = flags | VJ_WALK_AFTER_KEY | VJ_WALK_WROTE_ANY;

    /* Fused member value: the word after a key is emitted without a
     * second dispatch pass. Scalars only; containers keep AFTER_KEY
     * set and fall through to the threaded handlers, which own their
     * open/push and children. The 64-byte gate covers every scalar's
     * tight footprint (21 digits, 2 quotes + len*6, 5 atoms). */
    int32_t vcur  = vj_tw_skip_seams_h(tape, base, shift, cur);
    uint64_t vw   = tape[base + vcur];
    uint64_t vtag = vw & 0xFF00000000000000ULL;
    if (LIKELY(bend - buf > 64)) {
      switch (vtag) {
      case VJ_TSTRING:
      case VJ_TSTR_RAW:
      case VJ_TSTR_FREE: {
        uint32_t voff      = (uint32_t)(vw & 0xFFFFFFFFu);
        uint32_t vlen      = (uint32_t)((vw >> 32) & 0xFFFFFF);
        const uint8_t *vsp = (vtag == VJ_TSTR_RAW) ? (src + voff) : (arena + voff);
        int64_t vneed      = (vtag == VJ_TSTRING) ? (int64_t)vlen * 6 : (int64_t)vlen;
        if (LIKELY(buf + 2 + vneed <= bend)) {
          if (vtag == VJ_TSTRING) {
            buf += vj_escape_string_fast_inline(buf, vsp, vlen);
          } else {
            *buf++ = '"';
            buf += vj_tw_copy_verbatim(buf, vsp, vlen);
            *buf++ = '"';
          }
          cur    = vcur + 1;
          wflags = (wflags & ~(uint32_t)VJ_WALK_AFTER_KEY) | VJ_WALK_PENDING_COMMA;
          flags  = wflags;
          break;
        }
        flags = wflags;
        break;
      }
      case VJ_TINT64:
        buf += write_int64(buf, (int64_t)tape[base + vcur + 1]);
        cur    = vcur + 2;
        wflags = (wflags & ~(uint32_t)VJ_WALK_AFTER_KEY) | VJ_WALK_PENDING_COMMA;
        flags  = wflags;
        break;
      case VJ_TUINT64:
        buf += write_uint64(buf, tape[base + vcur + 1]);
        cur    = vcur + 2;
        wflags = (wflags & ~(uint32_t)VJ_WALK_AFTER_KEY) | VJ_WALK_PENDING_COMMA;
        flags  = wflags;
        break;
      case VJ_TTRUE:
        __builtin_memcpy(buf, "true", 4);
        buf += 4;
        cur    = vcur + 1;
        wflags = (wflags & ~(uint32_t)VJ_WALK_AFTER_KEY) | VJ_WALK_PENDING_COMMA;
        flags  = wflags;
        break;
      case VJ_TFALSE:
        __builtin_memcpy(buf, "false", 5);
        buf += 5;
        cur    = vcur + 1;
        wflags = (wflags & ~(uint32_t)VJ_WALK_AFTER_KEY) | VJ_WALK_PENDING_COMMA;
        flags  = wflags;
        break;
      case VJ_TNULL:
        __builtin_memcpy(buf, "null", 4);
        buf += 4;
        cur    = vcur + 1;
        wflags = (wflags & ~(uint32_t)VJ_WALK_AFTER_KEY) | VJ_WALK_PENDING_COMMA;
        flags  = wflags;
        break;
      default:
        /* Container or unknown: leave AFTER_KEY set for the handlers. */
        flags = wflags;
        break;
      }
    } else {
      flags = wflags;
    }
  } else {
    cur++;
    flags = (flags & ~(uint32_t)VJ_WALK_AFTER_KEY) | VJ_WALK_PENDING_COMMA | VJ_WALK_WROTE_ANY;
  }
  VJ_TW_NEXT();
}

/* Offset 0 targets the base label, so the anchor sits at the head of the
 * unknown-tag handler: every unlisted tag byte lands here. */
vj_tw_dispatch_base:
L_unknown: {
  /* Unknown tag: the tape format is append-only and unknown tags are
   * single-word by contract, so walking past keeps older readers
   * intact. Emit nothing. */
  cur++;
  VJ_TW_NEXT();
}

done:
  f->walk.cursor    = cur;
  f->walk.kindstack = kinds;
  f->walk.flags     = flags;
  *bufp             = buf;
  return VJ_TAPE_WALK_DONE;

buf_full:
  f->walk.cursor    = cur;
  f->walk.kindstack = kinds;
  f->walk.flags     = flags;
  *bufp             = buf;
  return VJ_TAPE_WALK_BUF_FULL;

#undef VJ_TW_DISPATCH
#undef VJ_TW_NEXT
#undef VJ_TW_PREFIX
}

#endif /* VJ_ENCVM_TAPEWALK_H */
