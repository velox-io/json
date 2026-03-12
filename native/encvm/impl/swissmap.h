/*
 * swissmap.h — Velox JSON C Engine: Swiss Map Iteration for map[string]string
 *
 * Out-of-line encoder for map[string]string values.
 * Marked noinline to keep the VM's hot dispatch loop compact.
 *
 * The function iterates over the Go runtime's Swiss Map data structures
 * (Map, table, group) and encodes all key-value pairs as JSON.
 *
 * Returns VjSwissMapResult with the advanced buffer pointer and an action
 * code indicating success or buffer-full (caller must save VM state and
 * return VJ_ERR_BUF_FULL, then resume).
 */

#ifndef VJ_ENCVM_SWISSMAP_H
#define VJ_ENCVM_SWISSMAP_H

#include "types.h"
#include "strfn.h"

// clang-format off

enum VjSwissMapAction {
  VJ_SWISS_DONE     = 0,  /* all entries encoded */
  VJ_SWISS_BUF_FULL = 1,  /* buffer insufficient — iteration state saved in frame */
};

/* Result struct — returned by value (fits in 2 registers). */
typedef struct {
  uint8_t *buf;       /* advanced buffer pointer */
  int32_t  action;    /* VjSwissMapAction */
} VjSwissMapResult;

/* Indent parameters passed from the VM to avoid macro dependencies. */
typedef struct {
  const uint8_t *indent_tpl;
  int16_t        indent_depth;
  uint8_t        indent_step;
  uint8_t        indent_prefix_len;
} VjSwissIndent;

/* Write indent: '\n' + prefix + indent for current depth. No-op if step==0. */
static inline uint8_t *
vj_swiss_write_indent(uint8_t *buf, const VjSwissIndent *ind) {
  if (ind->indent_step) {
    int n = 1 + ind->indent_prefix_len + ind->indent_depth * ind->indent_step;
    __builtin_memcpy(buf, ind->indent_tpl, n);
    buf += n;
  }
  return buf;
}

/* Max indent bytes for buffer check. Returns 0 if step==0. */
static inline int
vj_swiss_indent_pad(const VjSwissIndent *ind) {
  return ind->indent_step
           ? (1 + ind->indent_prefix_len + ind->indent_depth * ind->indent_step)
           : 0;
}

/*
 * vj_swiss_encode_one — encode a single map entry (key + ":" + value).
 * Returns advanced buf pointer. Caller must have checked buffer space.
 */
static inline uint8_t *
vj_swiss_encode_one(uint8_t *buf, const GoString *k, const GoString *v,
                    int *entry_first, uint32_t flags,
                    const VjSwissIndent *ind) {
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

/*
 * vj_swiss_map_iterate — iterate a Swiss Map and encode all entries as JSON.
 *
 * On first call, frame->map must be initialized (map_ptr, remaining, indices=0).
 * On resume after BUF_FULL, the saved indices in frame->map are used to continue.
 *
 * Parameters:
 *   buf, bend     — output buffer range
 *   frame         — VJ_FRAME_MAP stack frame (iteration state)
 *   entry_first   — whether the next entry is the first in the object
 *   flags         — VjEncFlags bitmask
 *   ind           — indent parameters
 *
 * Returns:
 *   .buf    — advanced buffer pointer
 *   .action — VJ_SWISS_DONE or VJ_SWISS_BUF_FULL
 *
 * On VJ_SWISS_BUF_FULL, frame->map.{dir_idx, group_idx, slot_idx, remaining}
 * are saved so the caller can resume after buffer growth.
 */
__attribute__((noinline)) static VjSwissMapResult
vj_swiss_map_iterate(uint8_t *buf, const uint8_t *bend,
                     VjStackFrame *frame, int entry_first,
                     uint32_t flags, const VjSwissIndent *ind) {
  const GoSwissMap *m = (const GoSwissMap *)frame->map.map_ptr;
  int ipad = vj_swiss_indent_pad(ind);
  int key_space = ind->indent_step ? 1 : 0;

  if (m->dir_len == 0) {
    /* === Small map: single inline group === */
    const uint8_t *group = (const uint8_t *)m->dir_ptr;
    uint64_t ctrls = *(const uint64_t *)group;
    int si = frame->map.slot_idx;

    while (frame->map.remaining > 0 && si < SWISS_GROUP_SLOTS) {
      uint8_t ctrl = (uint8_t)(ctrls >> (si * 8));
      if (ctrl & SWISS_CTRL_EMPTY) {
        si++;
        continue;
      }
      const GoString *k = (const GoString *)(group + SWISS_CTRL_SIZE + si * SWISS_SLOT_SIZE);
      const GoString *v = (const GoString *)(group + SWISS_CTRL_SIZE + si * SWISS_SLOT_SIZE + SWISS_ELEM_OFF);

      int64_t need = 1 + ipad + key_space + 2 + (k->len * 6) + 1 + 2 + (v->len * 6);
      if (__builtin_expect(buf + need > bend, 0)) {
        frame->map.slot_idx = si;
        return (VjSwissMapResult){buf, VJ_SWISS_BUF_FULL};
      }

      buf = vj_swiss_encode_one(buf, k, v, &entry_first, flags, ind);
      frame->map.remaining--;
      si++;
    }
  } else {
    /* === Large map: directory → tables → groups → slots === */
    int32_t di = frame->map.dir_idx;
    int32_t gi = frame->map.group_idx;
    int32_t si = frame->map.slot_idx;

    while (di < (int32_t)m->dir_len && frame->map.remaining > 0) {
      const GoSwissTable *tab = ((const GoSwissTable **)m->dir_ptr)[di];
      uint64_t num_groups = tab->groups_mask + 1;

      while ((uint64_t)gi < num_groups && frame->map.remaining > 0) {
        const uint8_t *group = (const uint8_t *)tab->groups_data + (uint64_t)gi * SWISS_GROUP_SIZE;
        uint64_t ctrls = *(const uint64_t *)group;

        while (si < SWISS_GROUP_SLOTS && frame->map.remaining > 0) {
          uint8_t ctrl = (uint8_t)(ctrls >> (si * 8));
          if (ctrl & SWISS_CTRL_EMPTY) {
            si++;
            continue;
          }
          const GoString *k = (const GoString *)(group + SWISS_CTRL_SIZE + si * SWISS_SLOT_SIZE);
          const GoString *v = (const GoString *)(group + SWISS_CTRL_SIZE + si * SWISS_SLOT_SIZE + SWISS_ELEM_OFF);

          int64_t need = 1 + ipad + key_space + 2 + (k->len * 6) + 1 + 2 + (v->len * 6);
          if (__builtin_expect(buf + need > bend, 0)) {
            frame->map.dir_idx = di;
            frame->map.group_idx = gi;
            frame->map.slot_idx = si;
            return (VjSwissMapResult){buf, VJ_SWISS_BUF_FULL};
          }

          buf = vj_swiss_encode_one(buf, k, v, &entry_first, flags, ind);
          frame->map.remaining--;
          si++;
        }
        si = 0;
        gi++;
      }
      gi = 0;
      /* Skip duplicate directory entries pointing to same table */
      {
        int skip = 1 << (m->global_depth - tab->local_depth);
        di += skip;
      }
    }
  }

  return (VjSwissMapResult){buf, VJ_SWISS_DONE};
}

#endif /* VJ_ENCVM_SWISSMAP_H */
