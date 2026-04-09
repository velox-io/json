/*
 * swissmap.h — Swiss Map iteration for map[string]string, map[string]int,
 * map[string]int64.
 *
 * Noinline encoders called from the VM dispatch loop.
 * Iterates Go runtime Swiss Map structures and encodes all KV pairs as JSON.
 *
 * Architecture: always_inline generic impl + noinline specialized wrappers.
 * Each wrapper passes constant layout params (slot_size, elem_off, group_size)
 * and known function addresses (encode_one, calc_need), so the compiler does
 * full constant folding and devirtualization — equivalent codegen to the old
 * macro-template approach, but readable and debuggable as normal C functions.
 */

#ifndef VJ_ENCVM_SWISSMAP_H
#define VJ_ENCVM_SWISSMAP_H

#include "number.h"
#include "strfn.h"
#include "types.h"
#include "vj_compat.h"

/* Swiss Map layout constants — shared across all variants. */
#define SWISS_GROUP_SLOTS 8
#define SWISS_CTRL_SIZE 8
#define SWISS_CTRL_EMPTY 0x80

/* Layout constants per variant (verified at Go init time). */
/* map[string]string: slot=32, elem_off=16, group=264 */
#define SWISS_STR_STR_SLOT_SIZE 32
#define SWISS_STR_STR_ELEM_OFF 16
#define SWISS_STR_STR_GROUP_SIZE 264

/* map[string]int and map[string]int64: slot=24, elem_off=16, group=200 */
#define SWISS_STR_INT_SLOT_SIZE 24
#define SWISS_STR_INT_ELEM_OFF 16
#define SWISS_STR_INT_GROUP_SIZE 200

/* Backward compat aliases used by existing code (= str_str layout). */
#define SWISS_SLOT_SIZE SWISS_STR_STR_SLOT_SIZE
#define SWISS_ELEM_OFF SWISS_STR_STR_ELEM_OFF
#define SWISS_GROUP_SIZE SWISS_STR_STR_GROUP_SIZE

/* Mirrors internal/runtime/maps.Map (48 bytes).
 * Offsets verified at Go init time (rt_internal.go). */
typedef struct GoSwissMap {
  uint64_t used;          /*  0 */
  uintptr_t seed;         /*  8 */
  void *dir_ptr;          /* 16: → group (small) or → *table[] (large) */
  int64_t dir_len;        /* 24: 0 = small map, else 1<<globalDepth */
  uint8_t global_depth;   /* 32 */
  uint8_t global_shift;   /* 33 */
  uint8_t writing;        /* 34 */
  uint8_t _pad_tombstone; /* 35 */
  uint32_t _pad36;        /* 36 */
  uint64_t clear_seq;     /* 40 */
} GoSwissMap;

_Static_assert(sizeof(GoSwissMap) == 48, "GoSwissMap must be 48 bytes");
_Static_assert(offsetof(GoSwissMap, used) == 0, "GoSwissMap.used offset");
_Static_assert(offsetof(GoSwissMap, dir_ptr) == 16,
               "GoSwissMap.dir_ptr offset");
_Static_assert(offsetof(GoSwissMap, dir_len) == 24,
               "GoSwissMap.dir_len offset");
_Static_assert(offsetof(GoSwissMap, global_depth) == 32,
               "GoSwissMap.global_depth offset");
_Static_assert(offsetof(GoSwissMap, clear_seq) == 40,
               "GoSwissMap.clear_seq offset");

/* Mirrors internal/runtime/maps.table (32 bytes). */
typedef struct GoSwissTable {
  uint16_t used;        /*  0 */
  uint16_t capacity;    /*  2 */
  uint16_t growth_left; /*  4 */
  uint8_t local_depth;  /*  6 */
  uint8_t _pad7;        /*  7 */
  int64_t index;        /*  8 */
  void *groups_data;    /* 16 */
  uint64_t groups_mask; /* 24: num_groups - 1 */
} GoSwissTable;

_Static_assert(sizeof(GoSwissTable) == 32, "GoSwissTable must be 32 bytes");
_Static_assert(offsetof(GoSwissTable, used) == 0, "GoSwissTable.used offset");
_Static_assert(offsetof(GoSwissTable, local_depth) == 6,
               "GoSwissTable.local_depth offset");
_Static_assert(offsetof(GoSwissTable, index) == 8, "GoSwissTable.index offset");
_Static_assert(offsetof(GoSwissTable, groups_data) == 16,
               "GoSwissTable.groups_data offset");
_Static_assert(offsetof(GoSwissTable, groups_mask) == 24,
               "GoSwissTable.groups_mask offset");

enum VjSwissMapAction {
  VJ_SWISS_DONE = 0,
  VJ_SWISS_BUF_FULL = 1,
};

typedef struct {
  uint8_t *buf;
  int32_t action;
} VjSwissMapResult;

typedef struct {
  const uint8_t *indent_tpl;
  int16_t indent_depth;
  uint8_t indent_step;
  uint8_t indent_prefix_len;
} VjSwissIndent;

static inline uint8_t *vj_swiss_write_indent(uint8_t *buf,
                                             const VjSwissIndent *ind) {
  if (ind->indent_step) {
    int n = 1 + ind->indent_prefix_len + ind->indent_depth * ind->indent_step;
    __builtin_memcpy(buf, ind->indent_tpl, n);
    buf += n;
  }
  return buf;
}

static inline int vj_swiss_indent_pad(const VjSwissIndent *ind) {
  return ind->indent_step ? (1 + ind->indent_prefix_len +
                             ind->indent_depth * ind->indent_step)
                          : 0;
}

/* --- Encode-one helpers and need calculators — one per variant --- */

/* map[string]string: key=GoString, val=GoString */
static inline uint8_t *
vj_swiss_encode_one_str_str(uint8_t *buf, const GoString *k,
                            const uint8_t *val_ptr, int *entry_first,
                            uint32_t flags, const VjSwissIndent *ind) {
  if (!*entry_first) {
    *buf++ = ',';
    buf = vj_swiss_write_indent(buf, ind);
  }
  *entry_first = 0;
  const GoString *v = (const GoString *)val_ptr;
#ifdef VJ_FAST_STRING_ESCAPE
  buf += VJ_ESCAPE_STRING_FAST_DISPATCH(buf, k->ptr, k->len);
  *buf++ = ':';
  if (ind->indent_step) {
    *buf++ = ' ';
  }
  buf += VJ_ESCAPE_STRING_FAST_DISPATCH(buf, v->ptr, v->len);
#else
  buf += VJ_ESCAPE_STRING_DISPATCH(buf, k->ptr, k->len, flags);
  *buf++ = ':';
  if (ind->indent_step) {
    *buf++ = ' ';
  }
  buf += VJ_ESCAPE_STRING_DISPATCH(buf, v->ptr, v->len, flags);
#endif
  return buf;
}

/* map[string]int / map[string]int64: key=GoString, val=int64 (8 bytes) */
static inline uint8_t *
vj_swiss_encode_one_str_int(uint8_t *buf, const GoString *k,
                            const uint8_t *val_ptr, int *entry_first,
                            uint32_t flags, const VjSwissIndent *ind) {
  if (!*entry_first) {
    *buf++ = ',';
    buf = vj_swiss_write_indent(buf, ind);
  }
  *entry_first = 0;
#ifdef VJ_FAST_STRING_ESCAPE
  buf += VJ_ESCAPE_STRING_FAST_DISPATCH(buf, k->ptr, k->len);
#else
  buf += VJ_ESCAPE_STRING_DISPATCH(buf, k->ptr, k->len, flags);
#endif
  *buf++ = ':';
  if (ind->indent_step) {
    *buf++ = ' ';
  }
  int64_t val = *(const int64_t *)val_ptr;
  buf += write_int64(buf, val);
  return buf;
}

/* Backward-compatible encode_one (delegates to str_str variant). */
static inline uint8_t *vj_swiss_encode_one(uint8_t *buf, const GoString *k,
                                           const GoString *v, int *entry_first,
                                           uint32_t flags,
                                           const VjSwissIndent *ind) {
  return vj_swiss_encode_one_str_str(buf, k, (const uint8_t *)v, entry_first,
                                     flags, ind);
}

/* Need calculators — static inline functions replacing the old macros. */
static inline int64_t vj_swiss_need_str_str(const GoString *k,
                                            const uint8_t *val_ptr, int ipad,
                                            int key_space) {
  /* comma + indent + space + key_quote(2) + key_escape + ':' + val_quote(2) +
   * val_escape */
  const GoString *v = (const GoString *)val_ptr;
  return 1 + ipad + key_space + 2 + (k->len * 6) + 1 + 2 + (v->len * 6);
}

static inline int64_t vj_swiss_need_str_int(const GoString *k,
                                            const uint8_t *val_ptr, int ipad,
                                            int key_space) {
  /* comma + indent + space + key_quote(2) + key_escape + ':' + max_int64(21) */
  (void)val_ptr;
  return 1 + ipad + key_space + 2 + (k->len * 6) + 1 + 21;
}

/* --- Callback types for encode-one and need-calc --- */
typedef uint8_t *(*vj_swiss_encode_fn)(uint8_t *buf, const GoString *k,
                                       const uint8_t *val_ptr, int *entry_first,
                                       uint32_t flags,
                                       const VjSwissIndent *ind);
typedef int64_t (*vj_swiss_need_fn)(const GoString *k, const uint8_t *val_ptr,
                                    int ipad, int key_space);

/* --- Core iteration logic (always_inline, parameterized).
 * Inlined into each noinline wrapper with constant params,
 * enabling full constant folding and devirtualization. --- */
INLINE VjSwissMapResult vj_swiss_iterate_impl(
    uint8_t *buf, const uint8_t *bend, VjStackFrame *frame, const GoSwissMap *m,
    int32_t remaining, int32_t di, int32_t gi, int32_t si, int entry_first,
    uint32_t flags, const VjSwissIndent *ind, const int32_t slot_size,
    const int32_t elem_off, const int32_t group_size,
    vj_swiss_encode_fn encode_one, vj_swiss_need_fn calc_need) {
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

      const uint8_t *slot = group + SWISS_CTRL_SIZE + si * slot_size;
      const GoString *k = (const GoString *)slot;
      const uint8_t *vp = slot + elem_off;

      int64_t need = calc_need(k, vp, ipad, key_space);
      if (__builtin_expect(buf + need > bend, 0)) {
        frame->map.map_ptr = m;
        frame->map.remaining = remaining;
        frame->map.slot_idx = (uint8_t)si;
        frame->map.dir_idx = 0;
        frame->map.group_idx = 0;
        frame->map.entry_first = (uint8_t)entry_first;
        return (VjSwissMapResult){buf, VJ_SWISS_BUF_FULL};
      }

      buf = encode_one(buf, k, vp, &entry_first, flags, ind);
      remaining--;
      si++;
    }
  } else {
    /* Large map: directory → tables → groups → slots */
    while (di < (int32_t)m->dir_len && remaining > 0) {
      const GoSwissTable *tab = ((const GoSwissTable **)m->dir_ptr)[di];
      uint64_t num_groups = tab->groups_mask + 1;

      while ((uint64_t)gi < num_groups && remaining > 0) {
        const uint8_t *group =
            (const uint8_t *)tab->groups_data + (uint64_t)gi * group_size;
        uint64_t ctrls = *(const uint64_t *)group;

        while (si < SWISS_GROUP_SLOTS && remaining > 0) {
          uint8_t ctrl = (uint8_t)(ctrls >> (si * 8));
          if (ctrl & SWISS_CTRL_EMPTY) {
            si++;
            continue;
          }

          const uint8_t *slot = group + SWISS_CTRL_SIZE + si * slot_size;
          const GoString *k = (const GoString *)slot;
          const uint8_t *vp = slot + elem_off;

          int64_t need = calc_need(k, vp, ipad, key_space);
          if (__builtin_expect(buf + need > bend, 0)) {
            frame->map.map_ptr = m;
            frame->map.remaining = remaining;
            frame->map.dir_idx = di;
            frame->map.group_idx = (uint8_t)gi;
            frame->map.slot_idx = (uint8_t)si;
            frame->map.entry_first = (uint8_t)entry_first;
            return (VjSwissMapResult){buf, VJ_SWISS_BUF_FULL};
          }

          buf = encode_one(buf, k, vp, &entry_first, flags, ind);
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

/* --- Specialized noinline wrappers — one per map variant --- */

/* map[string]string */
NOINLINE static VjSwissMapResult
vj_swiss_iterate_str_str(uint8_t *buf, const uint8_t *bend, VjStackFrame *frame,
                         const GoSwissMap *m, int32_t remaining, int32_t di,
                         int32_t gi, int32_t si, int entry_first,
                         uint32_t flags, const VjSwissIndent *ind) {
  return vj_swiss_iterate_impl(
      buf, bend, frame, m, remaining, di, gi, si, entry_first, flags, ind,
      SWISS_STR_STR_SLOT_SIZE, SWISS_STR_STR_ELEM_OFF, SWISS_STR_STR_GROUP_SIZE,
      vj_swiss_encode_one_str_str, vj_swiss_need_str_str);
}

/* map[string]int / map[string]int64 */
NOINLINE static VjSwissMapResult
vj_swiss_iterate_str_int(uint8_t *buf, const uint8_t *bend, VjStackFrame *frame,
                         const GoSwissMap *m, int32_t remaining, int32_t di,
                         int32_t gi, int32_t si, int entry_first,
                         uint32_t flags, const VjSwissIndent *ind) {
  return vj_swiss_iterate_impl(
      buf, bend, frame, m, remaining, di, gi, si, entry_first, flags, ind,
      SWISS_STR_INT_SLOT_SIZE, SWISS_STR_INT_ELEM_OFF, SWISS_STR_INT_GROUP_SIZE,
      vj_swiss_encode_one_str_int, vj_swiss_need_str_int);
}

/* Backward-compatible wrapper. */
NOINLINE static VjSwissMapResult
vj_swiss_map_iterate(uint8_t *buf, const uint8_t *bend, VjStackFrame *frame,
                     const GoSwissMap *m, int32_t remaining, int32_t di,
                     int32_t gi, int32_t si, int entry_first, uint32_t flags,
                     const VjSwissIndent *ind) {
  return vj_swiss_iterate_str_str(buf, bend, frame, m, remaining, di, gi, si,
                                  entry_first, flags, ind);
}

/* --- vj_swiss_next_full_slot — generic slot scanner for MAP_STR_ITER.
 * Scans forward to the next full slot. Returns slot pointer or NULL. --- */
NOINLINE static const uint8_t *vj_swiss_next_full_slot(const GoSwissMap *m,
                                                       int32_t slot_size,
                                                       int32_t *di, int32_t *gi,
                                                       int32_t *si) {
  int32_t group_size = SWISS_CTRL_SIZE + SWISS_GROUP_SLOTS * slot_size;

  if (m->dir_len == 0) {
    /* Small map: single inline group */
    const uint8_t *group = (const uint8_t *)m->dir_ptr;
    uint64_t ctrls = *(const uint64_t *)group;
    int32_t s = *si;
    while (s < SWISS_GROUP_SLOTS) {
      uint8_t ctrl = (uint8_t)(ctrls >> (s * 8));
      if (!(ctrl & SWISS_CTRL_EMPTY)) {
        *si = s;
        return group + SWISS_CTRL_SIZE + s * slot_size;
      }
      s++;
    }
    return NULL;
  }

  /* Large map: directory → tables → groups → slots */
  int32_t d = *di, g = *gi, s = *si;
  while (d < (int32_t)m->dir_len) {
    const GoSwissTable *tab = ((const GoSwissTable **)m->dir_ptr)[d];
    uint64_t num_groups = tab->groups_mask + 1;

    while ((uint64_t)g < num_groups) {
      const uint8_t *group =
          (const uint8_t *)tab->groups_data + (uint64_t)g * group_size;
      uint64_t ctrls = *(const uint64_t *)group;

      while (s < SWISS_GROUP_SLOTS) {
        uint8_t ctrl = (uint8_t)(ctrls >> (s * 8));
        if (!(ctrl & SWISS_CTRL_EMPTY)) {
          *di = d;
          *gi = g;
          *si = s;
          return group + SWISS_CTRL_SIZE + s * slot_size;
        }
        s++;
      }
      s = 0;
      g++;
    }
    g = 0;
    {
      int skip = 1 << (m->global_depth - tab->local_depth);
      d += skip;
    }
  }
  return NULL;
}

#endif /* VJ_ENCVM_SWISSMAP_H */
