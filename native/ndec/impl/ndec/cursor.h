#ifndef NDEC_CURSOR_H
#define NDEC_CURSOR_H

#include <stdint.h>
#include "ndec/core/tape.h" /* TAPE_PAYLOAD_MASK */

#define IDX_PEEK() ({ src[*cursor]; })
#define IDX_PTR()  ({ src + *cursor; })
#define IDX_POS()  (*cursor)
#define IDX_EOF()  (cursor >= m->idx_end)
#define IDX_ADVANCE()                                                                                             \
  ({                                                                                                              \
    const uint8_t *_p = src + *cursor;                                                                            \
    cursor++;                                                                                                     \
    _p;                                                                                                           \
  })
#define IDX_ADVANCE_CHAR()                                                                                        \
  ({                                                                                                              \
    uint8_t _c = src[*cursor];                                                                                    \
    cursor++;                                                                                                     \
    _c;                                                                                                           \
  })
#define IDX_CONSUME() (cursor++)
#define IDX_EXPECT(c)                                                                                             \
  do {                                                                                                            \
    if (src[*cursor++] != (c)) BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, 0);                                             \
  } while (0)
#define IDX_ACCEPT(c)                                                                                             \
  ({                                                                                                              \
    int _r = (src[*cursor] == (c));                                                                               \
    if (_r) cursor++;                                                                                             \
    _r;                                                                                                           \
  })

/* Tape binding reuses cursor storage. Reads use a uint64_t view; advances assign
 * cursor so the canonical resumable position stays synchronized. */
#define TAP_CURSOR ((const uint64_t *)cursor)

/* Build the active tape view from resumable machine state. The mode packs seam
 * projection with descriptor flags, so readers mask the projection bits. */
#define TAP_VIEW()                                                                                                \
  tape_view(m->b.alloc.value_tape, (const uint64_t *)m->idx_end, m->tape_view_mode &TAPE_VIEW_SHIFT_MASK)

#define TAP_PEEK()    (*TAP_CURSOR)
#define TAP_TAG()     ((uint8_t)(*TAP_CURSOR >> 56))
#define TAP_PAYLOAD() (*TAP_CURSOR & TAPE_PAYLOAD_MASK)

#define TAP_ADVANCE()                                                                                             \
  (cursor = (const uint32_t *)tape_seam_skip(TAP_CURSOR + 1, (const uint64_t *)m->idx_end,                        \
                                             m->tape_view_mode & TAPE_VIEW_SHIFT_MASK))
#define TAP_EOF() (TAP_CURSOR >= (const uint64_t *)m->idx_end)

/* Consume an l/u/d pair by reading its arbitrary value word directly. Seam
 * following resumes after the pair at the next tag position. */
#define TAP_READ_NUMBER()                                                                                         \
  ({                                                                                                              \
    uint64_t _v = TAP_CURSOR[1]; /* the value word, read without inspecting it */                                 \
    cursor      = (const uint32_t *)tape_seam_skip(TAP_CURSOR + 2, (const uint64_t *)m->idx_end,                  \
                                                   m->tape_view_mode & TAPE_VIEW_SHIFT_MASK);                     \
    _v;                                                                                                           \
  })

#define TAP_FOLLOW_SEAMS()                                                                                        \
  (cursor = (const uint32_t *)tape_seam_skip(TAP_CURSOR, (const uint64_t *)m->idx_end,                            \
                                             m->tape_view_mode & TAPE_VIEW_SHIFT_MASK))

#define TAP_SKIP_VALUE() (cursor = (const uint32_t *)tape_skip_value(TAP_CURSOR, TAP_VIEW()))

#endif /* NDEC_CURSOR_H */
