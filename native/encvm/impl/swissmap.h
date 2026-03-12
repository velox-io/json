/*
 * swissmap.h — Swiss Map iteration for map[string]string.
 *
 * Noinline encoder called from the VM dispatch loop.
 * Iterates Go runtime Swiss Map structures and encodes all KV pairs as JSON.
 */

#ifndef VJ_ENCVM_SWISSMAP_H
#define VJ_ENCVM_SWISSMAP_H

#include "strfn.h"
#include "types.h"

// clang-format off

/* Swiss Map constants for map[string]string (inline slots). */
#define SWISS_GROUP_SLOTS     8
#define SWISS_CTRL_SIZE       8
#define SWISS_SLOT_SIZE       32
#define SWISS_ELEM_OFF        16
#define SWISS_GROUP_SIZE      264    /* CTRL_SIZE + GROUP_SLOTS * SLOT_SIZE */
#define SWISS_CTRL_EMPTY      0x80

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

static inline uint8_t *
vj_swiss_encode_one(uint8_t *buf, const GoString *k, const GoString *v,
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
#ifdef VJ_FAST_STRING_ESCAPE
  buf += vj_escape_string_fast(buf, (const uint8_t *)v->ptr, v->len);
#else
  buf += vj_escape_string(buf, (const uint8_t *)v->ptr, v->len, flags);
#endif
  return buf;
}

__attribute__((noinline)) static VjSwissMapResult
vj_swiss_map_iterate(uint8_t *buf, const uint8_t *bend,
                     VjStackFrame *frame,
                     const GoSwissMap *m, int32_t remaining,
                     int32_t di, int32_t gi, int32_t si,
                     int entry_first,
                     uint32_t flags, const VjSwissIndent *ind) {
  int ipad = vj_swiss_indent_pad(ind);
  int key_space = ind->indent_step ? 1 : 0;

  if (m->dir_len == 0) {
    /* Small map: single inline group */
    const uint8_t *group = (const uint8_t *)m->dir_ptr;
    uint64_t ctrls = *(const uint64_t *)group;

    while (remaining > 0 && si < SWISS_GROUP_SLOTS) {
      uint8_t ctrl = (uint8_t)(ctrls >> (si * 8));
      if (ctrl & SWISS_CTRL_EMPTY) {
        si++;
        continue;
      }
      const GoString *k = (const GoString *)(group + SWISS_CTRL_SIZE + si * SWISS_SLOT_SIZE);
      const GoString *v = (const GoString *)(group + SWISS_CTRL_SIZE + si * SWISS_SLOT_SIZE + SWISS_ELEM_OFF);

      int64_t need = 1 + ipad + key_space + 2 + (k->len * 6) + 1 + 2 + (v->len * 6);
      if (__builtin_expect(buf + need > bend, 0)) {
        frame->map.map_ptr = m;
        frame->map.remaining = remaining;
        frame->map.slot_idx = si;
        frame->map.dir_idx = 0;
        frame->map.group_idx = 0;
        return (VjSwissMapResult){buf, VJ_SWISS_BUF_FULL};
      }

      buf = vj_swiss_encode_one(buf, k, v, &entry_first, flags, ind);
      remaining--;
      si++;
    }
  } else {
    /* Large map: directory → tables → groups → slots */
    while (di < (int32_t)m->dir_len && remaining > 0) {
      const GoSwissTable *tab = ((const GoSwissTable **)m->dir_ptr)[di];
      uint64_t num_groups = tab->groups_mask + 1;

      while ((uint64_t)gi < num_groups && remaining > 0) {
        const uint8_t *group = (const uint8_t *)tab->groups_data + (uint64_t)gi * SWISS_GROUP_SIZE;
        uint64_t ctrls = *(const uint64_t *)group;

        while (si < SWISS_GROUP_SLOTS && remaining > 0) {
          uint8_t ctrl = (uint8_t)(ctrls >> (si * 8));
          if (ctrl & SWISS_CTRL_EMPTY) {
            si++;
            continue;
          }
          const GoString *k = (const GoString *)(group + SWISS_CTRL_SIZE + si * SWISS_SLOT_SIZE);
          const GoString *v = (const GoString *)(group + SWISS_CTRL_SIZE + si * SWISS_SLOT_SIZE + SWISS_ELEM_OFF);

          int64_t need = 1 + ipad + key_space + 2 + (k->len * 6) + 1 + 2 + (v->len * 6);
          if (__builtin_expect(buf + need > bend, 0)) {
            frame->map.map_ptr = m;
            frame->map.remaining = remaining;
            frame->map.dir_idx = di;
            frame->map.group_idx = gi;
            frame->map.slot_idx = si;
            return (VjSwissMapResult){buf, VJ_SWISS_BUF_FULL};
          }

          buf = vj_swiss_encode_one(buf, k, v, &entry_first, flags, ind);
          remaining--;
          si++;
        }
        si = 0;
        gi++;
      }
      gi = 0;
      {
        int skip = 1 << (m->global_depth - tab->local_depth);
        di += skip;
      }
    }
  }

  return (VjSwissMapResult){buf, VJ_SWISS_DONE};
}

#endif /* VJ_ENCVM_SWISSMAP_H */
