#ifndef NDEC_TYPES_H
#define NDEC_TYPES_H

#include <stddef.h>
#include <stdint.h>

#include "ndec/core/helper.h" // IWYU pragma: keep

#define NDEC_MAX_DEPTH 256

enum NdecExit {
  NDEC_OK           = 0,
  NDEC_SUSPEND      = 1,
  NDEC_ERR_SYNTAX   = 2,
  NDEC_ERR_EOF      = 3,
  NDEC_ERR_DEPTH    = 4,
  NDEC_ERR_KEYWORD  = 5,
  NDEC_ERR_TRAILING = 6,
};

/* Each frame's phase describes "what this frame does next".
 * A parent writes its own continuation phase before STACK_PUSH; the
 * child's end hook (end_object / end_array) pops the stack and
 * dispatches via frames[depth - 1].phase. */
enum NdecPhase {
  NDEC_PHASE_ROOT_VALUE             = 0,
  NDEC_PHASE_OBJECT_FIELD_OR_END    = 1,
  NDEC_PHASE_OBJECT_FIELD_VALUE     = 2,
  NDEC_PHASE_OBJECT_CONTINUE_OR_END = 3,
  NDEC_PHASE_ARRAY_ELEM_OR_END      = 4,
  NDEC_PHASE_ARRAY_ELEM_VALUE       = 5,
  NDEC_PHASE_ARRAY_CONTINUE_OR_END  = 6,
  NDEC_PHASE_ROOT_DONE              = 7,
  NDEC_PHASE_SKIP_VALUE             = 8,

  NDEC_PHASE_COUNT = 9,
};

/* Cross-chunk carry state for the SIMD scanner.
 *
 * `is_final` lives here (not in NdecCtx) so ndec_advance_chunk can
 * read it through the state pointer it already carries. It is set by
 * ndec_ctx_set_input and read by the scanner and by cold suspend /
 * keyword-edge paths. Otherwise treat it as read-only. */
typedef struct NdecScanState {
  uint64_t prev_in_string;        /* 0 or ~0 */
  uint64_t prev_escape;           /* 0 or 1 */
  uint64_t prev_structural_or_ws; /* 0 or 1 */
  uint64_t last_backslash;        /* backslash bitmap from last ndec_scan_chunk call */
  uint32_t is_final;
} NdecScanState;

/* Reactor directives (return values from callbacks):
 *   NDEC_PROCEED  (0)     continue parsing
 *   NDEC_SKIP     (1)     skip the upcoming value
 *   NDEC_YIELD    (-1)    suspend, return control to caller
 *   value <= -2           reactor error (user-defined)
 *
 * NDEC_YIELD is a negative sentinel so it shares the hot-path
 * `directive < 0` branch with error codes at zero extra cost; the
 * cold path distinguishes NDEC_YIELD from user errors. User-defined
 * error codes must be strictly less than NDEC_YIELD (i.e. <= -2) to
 * remain distinguishable. */
#define NDEC_PROCEED 0
#define NDEC_SKIP    1
#define NDEC_YIELD   (-1)

typedef struct NdecRawStr {
  const uint8_t *ptr;
  uint32_t len;
} NdecRawStr;

/* Extended string info for callbacks that need escape metadata.
 * `has_escape` is non-zero iff the string content contains at least one
 * backslash escape sequence. Only scalar_string and object_field
 * receive this; scalar_number stays with NdecRawStr. */
typedef struct NdecStrInfo {
  NdecRawStr raw;
  uint8_t has_escape;
} NdecStrInfo;

/* Forward declaration: NdecReactor uses NdecCtx * in its vtable. */
typedef struct NdecCtx NdecCtx;

/* Reactor vtable. A NULL reactor means validate-only; individual
 * function pointers may also be NULL (treated as PROCEED). */
typedef struct NdecReactor {
  int32_t (*begin_object)(NdecCtx *ctx, void *ud, uint32_t child_phase);
  int32_t (*end_object)(NdecCtx *ctx, void *ud);
  int32_t (*object_field)(NdecCtx *ctx, void *ud, NdecStrInfo key);
  int32_t (*begin_array)(NdecCtx *ctx, void *ud, uint32_t child_phase);
  int32_t (*end_array)(NdecCtx *ctx, void *ud);
  int32_t (*scalar_null)(NdecCtx *ctx, void *ud);
  int32_t (*scalar_bool)(NdecCtx *ctx, void *ud, int value);
  int32_t (*scalar_number)(NdecCtx *ctx, void *ud, NdecRawStr raw);
  int32_t (*scalar_string)(NdecCtx *ctx, void *ud, NdecStrInfo str);
} NdecReactor;

/* NdecFrame extension point.
 *
 * Hosts that need per frame state (like the Go binding's typeinfo,
 * destination pointer, container kind) inject extra fields by
 * defining NDEC_FRAME_EXTRA_FIELDS before including this header. The
 * kernel never reads or writes the extra fields; STACK_PUSH only
 * initializes phase and data, leaving extras for the host's reactor
 * hooks.
 *
 * Example (in a host translation unit, before #include "types.h"):
 *
 *   #define NDEC_FRAME_EXTRA_FIELDS \
 *     const MyTypeInfo *type;       \
 *     uint8_t *dst;                 \
 *     int32_t pending_field_idx;
 *
 * Default expansion is empty so unmodified hosts see the original
 * 8 byte NdecFrame. */
#ifndef NDEC_FRAME_EXTRA_FIELDS
#define NDEC_FRAME_EXTRA_FIELDS
#endif

typedef struct NdecFrame {
  uint32_t phase;
  uint32_t data; /* scratch slot; currently used only by SKIP_VALUE to
                  * persist skip_depth across suspend/resume */
  NDEC_FRAME_EXTRA_FIELDS
} NdecFrame;

typedef struct NdecCtx {
  const uint8_t *buf;
  const uint8_t *buf_end;

  const NdecReactor *reactor;
  void *user_data;

  /* Hot cursor state (loaded into registers at entry, saved on exit) */
  const uint8_t *cur_pos;
  const uint8_t *chunk_ptr;
  uint64_t structural_bits;

  NdecScanState scan_state;

  uint32_t exit_code;
  uint32_t error_pos;

  int32_t sp; /* stack top index; -1 before bootstrap, 0 after */
  NdecFrame frames[NDEC_MAX_DEPTH];
} NdecCtx;

static inline void ndec_ctx_init(NdecCtx *ctx, const NdecReactor *reactor, void *user_data) {
  ctx->buf             = NULL;
  ctx->buf_end         = NULL;
  ctx->reactor         = reactor;
  ctx->user_data       = user_data;
  ctx->cur_pos         = NULL;
  ctx->chunk_ptr       = NULL;
  ctx->structural_bits = 0;
  ctx->exit_code       = 0;
  ctx->error_pos       = 0;
  ctx->sp               = -1;
  ctx->frames[0].data    = 0;
  /* frames[0].phase is left uninitialized; host must call
   * ndec_ctx_arm_root before first parse. The extras fields of
   * frames[0] must be written by host before arm_root. */

  ctx->scan_state.prev_in_string        = 0;
  ctx->scan_state.prev_escape           = 0;
  ctx->scan_state.prev_structural_or_ws = 1;
  ctx->scan_state.last_backslash        = 0;
  ctx->scan_state.is_final              = 0;
}

/* Only updates buf / buf_end / is_final. The scanner state (cur_pos,
 * chunk_ptr, structural_bits, scan_state) is preserved for resume. */
static inline void ndec_ctx_set_input(NdecCtx *ctx, const uint8_t *buf, uint32_t len, int is_final) {
  ctx->buf                 = buf;
  ctx->buf_end             = buf + len;
  ctx->scan_state.is_final = is_final ? 1 : 0;
}

/* Arming the root frame for parsing.
 *
 * Precondition: host has written the root binding into
 * ctx->frames[0].extras (bind_type, bind_dst, bind_container_kind
 * etc.). This call installs sp = 0 and phase = ROOT_VALUE so the
 * parser starts on the root value state machine.
 *
 * Must be called after ndec_ctx_set_input and before the first
 * ndec_parse invocation. For resume scenarios (re-entering the same
 * parse after a yield), do NOT call this again; sp and phase are
 * already in the correct post-yield state. */
static inline void ndec_ctx_arm_root(NdecCtx *ctx) {
  /* sp stays at -1 (set by ndec_ctx_init). The parser's first-call
   * bootstrap path detects sp < 0 and installs sp = 0. This function
   * only writes the root frame's phase and data; extras must be
   * host-written before this call. */
  ctx->frames[0].phase   = NDEC_PHASE_ROOT_VALUE;
  ctx->frames[0].data    = 0;
  /* extras fields (bind_type, bind_dst, etc.) are host-written and preserved */
}

/* Reactor-facing stack helpers.
 *
 * ndec_stack_push: reactor calls this in begin_object/begin_array to
 * allocate and initialize a new frame. Returns NDEC_PROCEED on success
 * or NDEC_ERR_DEPTH on depth overflow. The reactor must call this after
 * it has computed child extras but before writing them to the new frame.
 *
 * ndec_stack_pop: reactor calls this in end_object/end_array to
 * release the current frame. The popped frame at frames[sp+1] is NOT
 * cleared; the reactor must read any needed data from it before or
 * immediately after pop (per I-X2). */
/* Negative reactor error codes (user-defined range: <= -3).
 * NDEC_ERR_DEPTH is returned as a negative reactor error from
 * ndec_stack_push so the parser's `directive < 0` check catches it. */
#define NDEC_ERR_REACTOR_DEPTH (-NDEC_ERR_DEPTH)

static inline int32_t ndec_stack_push(NdecCtx *ctx, uint32_t child_phase) {
  if (ctx->sp >= NDEC_MAX_DEPTH - 1)
    return NDEC_ERR_REACTOR_DEPTH;
  ctx->sp++;
  ctx->frames[ctx->sp].phase = child_phase;
  ctx->frames[ctx->sp].data  = 0;
  return NDEC_PROCEED;
}

static inline void ndec_stack_pop(NdecCtx *ctx) {
  ctx->sp--;
}

#endif /* NDEC_TYPES_H */
