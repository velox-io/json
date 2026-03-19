/*
 * swissmap.h — Swiss Map iteration for map[string]string, map[string]int, map[string]int64.
 *
 * Noinline encoders called from the VM dispatch loop.
 * Iterates Go runtime Swiss Map structures and encodes all KV pairs as JSON.
 *
 * Uses a macro template (VJ_DEFINE_SWISS_ITERATE) to generate specialized
 * iterate functions for each map variant, avoiding code duplication while
 * keeping the encoding logic (val read + val encode + need calc) specialized.
 */

#ifndef VJ_ENCVM_SWISSMAP_H
#define VJ_ENCVM_SWISSMAP_H

#include "strfn.h"
#include "number.h"
#include "types.h"

// clang-format off

/* Swiss Map layout constants — shared across all variants. */
#define SWISS_GROUP_SLOTS     8
#define SWISS_CTRL_SIZE       8
#define SWISS_CTRL_EMPTY      0x80

/* Layout constants per variant (verified at Go init time). */
/* map[string]string: slot=32, elem_off=16, group=264 */
#define SWISS_STR_STR_SLOT_SIZE    32
#define SWISS_STR_STR_ELEM_OFF     16
#define SWISS_STR_STR_GROUP_SIZE   264

/* map[string]int and map[string]int64: slot=24, elem_off=16, group=200 */
#define SWISS_STR_INT_SLOT_SIZE    24
#define SWISS_STR_INT_ELEM_OFF     16
#define SWISS_STR_INT_GROUP_SIZE   200

/* Backward compat aliases used by existing code (= str_str layout). */
#define SWISS_SLOT_SIZE       SWISS_STR_STR_SLOT_SIZE
#define SWISS_ELEM_OFF        SWISS_STR_STR_ELEM_OFF
#define SWISS_GROUP_SIZE      SWISS_STR_STR_GROUP_SIZE

/* Mirrors internal/runtime/maps.Map (48 bytes).
 * Offsets verified at Go init time (rt_internal.go). */
typedef struct GoSwissMap {
  uint64_t  used;           /*  0 */
  uintptr_t seed;           /*  8 */
  void     *dir_ptr;        /* 16: → group (small) or → *table[] (large) */
  int64_t   dir_len;        /* 24: 0 = small map, else 1<<globalDepth */
  uint8_t   global_depth;   /* 32 */
  uint8_t   global_shift;   /* 33 */
  uint8_t   writing;        /* 34 */
  uint8_t   _pad_tombstone; /* 35 */
  uint32_t  _pad36;         /* 36 */
  uint64_t  clear_seq;      /* 40 */
} GoSwissMap;

_Static_assert(sizeof(GoSwissMap) == 48, "GoSwissMap must be 48 bytes");
_Static_assert(offsetof(GoSwissMap, used) == 0, "GoSwissMap.used offset");
_Static_assert(offsetof(GoSwissMap, dir_ptr) == 16, "GoSwissMap.dir_ptr offset");
_Static_assert(offsetof(GoSwissMap, dir_len) == 24, "GoSwissMap.dir_len offset");
_Static_assert(offsetof(GoSwissMap, global_depth) == 32, "GoSwissMap.global_depth offset");
_Static_assert(offsetof(GoSwissMap, clear_seq) == 40, "GoSwissMap.clear_seq offset");

/* Mirrors internal/runtime/maps.table (32 bytes). */
typedef struct GoSwissTable {
  uint16_t  used;           /*  0 */
  uint16_t  capacity;       /*  2 */
  uint16_t  growth_left;    /*  4 */
  uint8_t   local_depth;    /*  6 */
  uint8_t   _pad7;          /*  7 */
  int64_t   index;          /*  8 */
  void     *groups_data;    /* 16 */
  uint64_t  groups_mask;    /* 24: num_groups - 1 */
} GoSwissTable;

_Static_assert(sizeof(GoSwissTable) == 32, "GoSwissTable must be 32 bytes");
_Static_assert(offsetof(GoSwissTable, used) == 0, "GoSwissTable.used offset");
_Static_assert(offsetof(GoSwissTable, local_depth) == 6, "GoSwissTable.local_depth offset");
_Static_assert(offsetof(GoSwissTable, index) == 8, "GoSwissTable.index offset");
_Static_assert(offsetof(GoSwissTable, groups_data) == 16, "GoSwissTable.groups_data offset");
_Static_assert(offsetof(GoSwissTable, groups_mask) == 24, "GoSwissTable.groups_mask offset");

enum VjSwissMapAction {
  VJ_SWISS_DONE     = 0,
  VJ_SWISS_BUF_FULL = 1,
};

typedef struct {
  uint8_t *buf;
  int32_t  action;
} VjSwissMapResult;

typedef struct {
  const uint8_t *indent_tpl;
  int16_t        indent_depth;
  uint8_t        indent_step;
  uint8_t        indent_prefix_len;
} VjSwissIndent;

static inline uint8_t * vj_swiss_write_indent(uint8_t *buf, const VjSwissIndent *ind) {
  if (ind->indent_step) {
    int n = 1 + ind->indent_prefix_len + ind->indent_depth * ind->indent_step;
    __builtin_memcpy(buf, ind->indent_tpl, n);
    buf += n;
  }
  return buf;
}

static inline int vj_swiss_indent_pad(const VjSwissIndent *ind) {
  return ind->indent_step
           ? (1 + ind->indent_prefix_len + ind->indent_depth * ind->indent_step)
           : 0;
}

/* ================================================================
 *  Encode-one helpers — one per variant
 * ================================================================ */

/* map[string]string: key=GoString, val=GoString */
static inline uint8_t *
vj_swiss_encode_one_str_str(uint8_t *buf, const GoString *k, const uint8_t *val_ptr,
                            int *entry_first, uint32_t flags, const VjSwissIndent *ind)
{
  if (!*entry_first) {
    *buf++ = ',';
    buf = vj_swiss_write_indent(buf, ind);
  }
  *entry_first = 0;
  const GoString *v = (const GoString *)val_ptr;
#ifdef VJ_FAST_STRING_ESCAPE
  buf += vj_escape_string_fast(buf, (const uint8_t *)k->ptr, k->len);
  *buf++ = ':';
  if (ind->indent_step) { *buf++ = ' '; }
  buf += vj_escape_string_fast(buf, (const uint8_t *)v->ptr, v->len);
#else
  buf += vj_escape_string(buf, (const uint8_t *)k->ptr, k->len, flags);
  *buf++ = ':';
  if (ind->indent_step) { *buf++ = ' '; }
  buf += vj_escape_string(buf, (const uint8_t *)v->ptr, v->len, flags);
#endif
  return buf;
}

/* map[string]int / map[string]int64: key=GoString, val=int64 (8 bytes) */
static inline uint8_t *
vj_swiss_encode_one_str_int(uint8_t *buf, const GoString *k, const uint8_t *val_ptr,
                            int *entry_first, uint32_t flags, const VjSwissIndent *ind)
{
  if (!*entry_first) {
    *buf++ = ',';
    buf = vj_swiss_write_indent(buf, ind);
  }
  *entry_first = 0;
#ifdef VJ_FAST_STRING_ESCAPE
  buf += vj_escape_string_fast(buf, (const uint8_t *)k->ptr, k->len);
#else
  buf += vj_escape_string(buf, (const uint8_t *)k->ptr, k->len, flags);
#endif
  *buf++ = ':';
  if (ind->indent_step) { *buf++ = ' '; }
  int64_t val = *(const int64_t *)val_ptr;
  buf += write_int64(buf, val);
  return buf;
}

/* ================================================================
 *  Backward-compatible encode_one (delegates to str_str variant)
 * ================================================================ */

static inline uint8_t *
vj_swiss_encode_one(uint8_t *buf, const GoString *k, const GoString *v,
                    int *entry_first, uint32_t flags, const VjSwissIndent *ind)
{
  return vj_swiss_encode_one_str_str(buf, k, (const uint8_t *)v, entry_first, flags, ind);
}

/* ================================================================
 *  Buffer-need calculators — one per variant
 * ================================================================ */

/* str_str: comma + indent + key_escape + ':' + space + val_escape */
#define VJ_SWISS_NEED_STR_STR(k, val_ptr, ipad, key_space) \
  (1 + (ipad) + (key_space) + 2 + ((k)->len * 6) + 1 + 2 + (((const GoString *)(val_ptr))->len * 6))

/* str_int: comma + indent + key_escape + ':' + space + max_int64 (21 digits) */
#define VJ_SWISS_NEED_STR_INT(k, val_ptr, ipad, key_space) \
  (1 + (ipad) + (key_space) + 2 + ((k)->len * 6) + 1 + 21)

/* ================================================================
 *  Macro template: generates a specialized iterate function
 *
 *  Parameters:
 *    FN_NAME       — function name (e.g. vj_swiss_iterate_str_str)
 *    SLOT_SIZE     — bytes per slot (32 for str_str, 24 for str_int)
 *    ELEM_OFF      — elem offset within slot (16 for all current variants)
 *    GROUP_SIZE    — bytes per group (CTRL_SIZE + GROUP_SLOTS * SLOT_SIZE)
 *    ENCODE_ONE    — encode function (e.g. vj_swiss_encode_one_str_str)
 *    CALC_NEED     — need macro (e.g. VJ_SWISS_NEED_STR_STR)
 * ================================================================ */

#define VJ_DEFINE_SWISS_ITERATE(FN_NAME, SLOT_SIZE, ELEM_OFF, GROUP_SIZE, \
                                ENCODE_ONE, CALC_NEED)                    \
__attribute__((noinline)) static VjSwissMapResult                         \
FN_NAME(uint8_t *buf, const uint8_t *bend,                               \
        VjStackFrame *frame,                                              \
        const GoSwissMap *m, int32_t remaining,                           \
        int32_t di, int32_t gi, int32_t si,                               \
        int entry_first,                                                  \
        uint32_t flags, const VjSwissIndent *ind) {                       \
  int ipad = vj_swiss_indent_pad(ind);                                    \
  int key_space = ind->indent_step ? 1 : 0;                              \
                                                                          \
  if (m->dir_len == 0) {                                                  \
    /* Small map: single inline group */                                  \
    const uint8_t *group = (const uint8_t *)m->dir_ptr;                   \
    uint64_t ctrls = *(const uint64_t *)group;                            \
                                                                          \
    while (remaining > 0 && si < SWISS_GROUP_SLOTS) {                     \
      uint8_t ctrl = (uint8_t)(ctrls >> (si * 8));                        \
      if (ctrl & SWISS_CTRL_EMPTY) {                                      \
        si++;                                                             \
        continue;                                                         \
      }                                                                   \
      const uint8_t *slot = group + SWISS_CTRL_SIZE + si * (SLOT_SIZE);   \
      const GoString *k = (const GoString *)slot;                         \
      const uint8_t *val_ptr = slot + (ELEM_OFF);                         \
                                                                          \
      int64_t need = CALC_NEED(k, val_ptr, ipad, key_space);             \
      if (__builtin_expect(buf + need > bend, 0)) {                       \
        frame->map.map_ptr = m;                                           \
        frame->map.remaining = remaining;                                 \
        frame->map.slot_idx = (uint8_t)si;                                \
        frame->map.dir_idx = 0;                                           \
        frame->map.group_idx = 0;                                         \
        return (VjSwissMapResult){buf, VJ_SWISS_BUF_FULL};                \
      }                                                                   \
                                                                          \
      buf = ENCODE_ONE(buf, k, val_ptr, &entry_first, flags, ind);       \
      remaining--;                                                        \
      si++;                                                               \
    }                                                                     \
  } else {                                                                \
    /* Large map: directory → tables → groups → slots */                  \
    while (di < (int32_t)m->dir_len && remaining > 0) {                   \
      const GoSwissTable *tab = ((const GoSwissTable **)m->dir_ptr)[di];  \
      uint64_t num_groups = tab->groups_mask + 1;                         \
                                                                          \
      while ((uint64_t)gi < num_groups && remaining > 0) {                \
        const uint8_t *group = (const uint8_t *)tab->groups_data          \
                               + (uint64_t)gi * (GROUP_SIZE);             \
        uint64_t ctrls = *(const uint64_t *)group;                        \
                                                                          \
        while (si < SWISS_GROUP_SLOTS && remaining > 0) {                 \
          uint8_t ctrl = (uint8_t)(ctrls >> (si * 8));                    \
          if (ctrl & SWISS_CTRL_EMPTY) {                                  \
            si++;                                                         \
            continue;                                                     \
          }                                                               \
          const uint8_t *slot = group + SWISS_CTRL_SIZE                   \
                                + si * (SLOT_SIZE);                       \
          const GoString *k = (const GoString *)slot;                     \
          const uint8_t *val_ptr = slot + (ELEM_OFF);                     \
                                                                          \
          int64_t need = CALC_NEED(k, val_ptr, ipad, key_space);         \
          if (__builtin_expect(buf + need > bend, 0)) {                   \
            frame->map.map_ptr = m;                                       \
            frame->map.remaining = remaining;                             \
            frame->map.dir_idx = di;                                      \
            frame->map.group_idx = (uint8_t)gi;                           \
            frame->map.slot_idx = (uint8_t)si;                            \
            return (VjSwissMapResult){buf, VJ_SWISS_BUF_FULL};            \
          }                                                               \
                                                                          \
          buf = ENCODE_ONE(buf, k, val_ptr, &entry_first, flags, ind);   \
          remaining--;                                                    \
          si++;                                                           \
        }                                                                 \
        si = 0;                                                           \
        gi++;                                                             \
      }                                                                   \
      gi = 0;                                                             \
      {                                                                   \
        int skip = 1 << (m->global_depth - tab->local_depth);            \
        di += skip;                                                       \
      }                                                                   \
    }                                                                     \
  }                                                                       \
                                                                          \
  return (VjSwissMapResult){buf, VJ_SWISS_DONE};                          \
}

/* ================================================================
 *  Instantiate specialized iterate functions
 * ================================================================ */

/* map[string]string */
VJ_DEFINE_SWISS_ITERATE(
  vj_swiss_iterate_str_str,
  SWISS_STR_STR_SLOT_SIZE, SWISS_STR_STR_ELEM_OFF, SWISS_STR_STR_GROUP_SIZE,
  vj_swiss_encode_one_str_str, VJ_SWISS_NEED_STR_STR)

/* map[string]int / map[string]int64 (identical layout: int and int64 are both 8 bytes on 64-bit) */
VJ_DEFINE_SWISS_ITERATE(
  vj_swiss_iterate_str_int,
  SWISS_STR_INT_SLOT_SIZE, SWISS_STR_INT_ELEM_OFF, SWISS_STR_INT_GROUP_SIZE,
  vj_swiss_encode_one_str_int, VJ_SWISS_NEED_STR_INT)

/* Backward-compatible wrapper — existing callers of vj_swiss_map_iterate
 * continue to work unchanged. */
__attribute__((noinline)) static VjSwissMapResult
vj_swiss_map_iterate(uint8_t *buf, const uint8_t *bend,
                     VjStackFrame *frame,
                     const GoSwissMap *m, int32_t remaining,
                     int32_t di, int32_t gi, int32_t si,
                     int entry_first,
                     uint32_t flags, const VjSwissIndent *ind) {
  return vj_swiss_iterate_str_str(buf, bend, frame, m, remaining,
                                  di, gi, si, entry_first, flags, ind);
}

#endif /* VJ_ENCVM_SWISSMAP_H */
