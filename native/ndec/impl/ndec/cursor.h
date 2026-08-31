#ifndef NDEC_CURSOR_H
#define NDEC_CURSOR_H

#include <stdint.h>
#include "ndec/core/tape.h" /* TAPE_PAYLOAD_MASK */

/* The walk reads structural characters through the index array the SIMD
 * pre-scan fills, so a single step reaches the next one and a token body never
 * needs skipping. SRC_POS reports a byte offset for error payloads.
 *
 * cursor_end counts the structurals the scan produced, not the readable slots:
 * the scanner appends three sentinel entries holding src_len and leaves them out
 * of the count, so object_continue and array_continue reach the 0x20 document
 * terminator without an at_end guard. EOF is "no real input remains", and the
 * walk may deliberately read past cursor_end to learn that. */
#define SRC_PEEK() (src[*cursor.idx])
#define SRC_PTR()  (src + *cursor.idx)
#define SRC_POS()  (*cursor.idx)
#define SRC_EOF()  (cursor.idx >= m->cursor_end.idx)

#define SRC_ADVANCE() (cursor.idx++)
#define SRC_ADVANCE_PTR()                                                                                         \
  ({                                                                                                              \
    const uint8_t *_p = src + *cursor.idx;                                                                        \
    cursor.idx++;                                                                                                 \
    _p;                                                                                                           \
  })
#define SRC_ADVANCE_CHAR()                                                                                        \
  ({                                                                                                              \
    uint8_t _c = src[*cursor.idx];                                                                                \
    cursor.idx++;                                                                                                 \
    _c;                                                                                                           \
  })
#define SRC_EXPECT(c)                                                                                             \
  do {                                                                                                            \
    if (src[*cursor.idx++] != (c)) BIND_YIELD_ERR(m, BIND_ERR_SYNTAX, 0);                                         \
  } while (0)
#define SRC_ACCEPT(c)                                                                                             \
  ({                                                                                                              \
    int _r = (src[*cursor.idx] == (c));                                                                           \
    if (_r) cursor.idx++;                                                                                         \
    _r;                                                                                                           \
  })

/* Tape binding reuses the same cursor word through its tape member. The phase
 * gate keeps the two families mutually exclusive, and every advance assigns
 * cursor so the canonical resumable position stays synchronized. */
#define TAP_CURSOR (cursor.tape)

/* Build the active tape view from resumable machine state. The mode packs seam
 * projection with descriptor flags, so readers mask the projection bits. */
#define TAP_VIEW() tape_view(m->b.alloc.value_tape, m->cursor_end.tape, m->tape_view_mode &TAPE_VIEW_SHIFT_MASK)

#define TAP_PEEK()    (*TAP_CURSOR)
#define TAP_TAG()     ((uint8_t)(*TAP_CURSOR >> 56))
#define TAP_PAYLOAD() (*TAP_CURSOR & TAPE_PAYLOAD_MASK)

#define TAP_ADVANCE()                                                                                             \
  (cursor.tape = tape_seam_skip(TAP_CURSOR + 1, m->cursor_end.tape, m->tape_view_mode & TAPE_VIEW_SHIFT_MASK))
#define TAP_EOF() (TAP_CURSOR >= m->cursor_end.tape)

/* Consume an l/u/d pair by reading its arbitrary value word directly. Seam
 * following resumes after the pair at the next tag position. */
#define TAP_READ_NUMBER()                                                                                         \
  ({                                                                                                              \
    uint64_t _v = TAP_CURSOR[1]; /* the value word, read without inspecting it */                                 \
    cursor.tape = tape_seam_skip(TAP_CURSOR + 2, m->cursor_end.tape, m->tape_view_mode & TAPE_VIEW_SHIFT_MASK);   \
    _v;                                                                                                           \
  })

#define TAP_FOLLOW_SEAMS()                                                                                        \
  (cursor.tape = tape_seam_skip(TAP_CURSOR, m->cursor_end.tape, m->tape_view_mode & TAPE_VIEW_SHIFT_MASK))

#define TAP_SKIP_VALUE() (cursor.tape = tape_skip_value(TAP_CURSOR, TAP_VIEW()))

#endif /* NDEC_CURSOR_H */
