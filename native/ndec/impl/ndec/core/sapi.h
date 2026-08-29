#ifndef __NDEC_CORE_SAPI_H__
#define __NDEC_CORE_SAPI_H__

#include <stdint.h>
#include "ndec/core/chunk.h"
#include "ndec/core/utf8.h"

#ifndef NDEC_FRAME_EXTRA_FIELDS
#define NDEC_FRAME_EXTRA_FIELDS
#endif

#define NDEC_MAX_DEPTH 256

enum NdecExit {
  NDEC_OK           = 0,
  NDEC_SUSPEND      = 1,
  NDEC_ERR_SYNTAX   = 2,
  NDEC_ERR_EOF      = 3,
  NDEC_ERR_DEPTH    = 4,
  NDEC_ERR_KEYWORD  = 5,
  NDEC_ERR_TRAILING = 6,
  NDEC_ERR_UTF8     = 7,
};

/* A parent records its continuation before pushing a child. The child's close
 * pops the frame and dispatches the recorded phase. */
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

typedef struct NdecFrame {
  uint32_t phase;
  uint32_t data; /* scratch slot; currently used only by SKIP_VALUE to
                  * persist skip_depth across suspend/resume */
  NDEC_FRAME_EXTRA_FIELDS
} NdecFrame;

/* Callback results select proceed, skip, suspend, or host error. Host errors
 * must be at most -2 so NDEC_YIELD remains distinct. */
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

/* Optional vtable for runtime function-pointer dispatch.
 *
 * The scanner body itself does not consult this vtable; every NDEC_R_*
 * default expands to NDEC_PROCEED. Hosts that prefer runtime dispatch
 * enable by defining NDEC_USE_VTABLE before include, which switches
 * in macro overrides that read ctx->reactor.
 */
typedef struct NdecReactor {
  int32_t (*begin_object)(void *ud);
  int32_t (*end_object)(void *ud);
  int32_t (*object_field)(void *ud, NdecStrInfo key);
  int32_t (*begin_array)(void *ud);
  int32_t (*end_array)(void *ud);
  int32_t (*scalar_null)(void *ud);
  int32_t (*scalar_bool)(void *ud, int value);
  int32_t (*scalar_number)(void *ud, NdecRawStr raw);
  int32_t (*scalar_string)(void *ud, NdecStrInfo str);
} NdecReactor;

typedef struct NdecSaxContext {
  const uint8_t *buf;
  const uint8_t *buf_end;
  uint32_t is_final; /* 1 iff `buf` is the last segment; set by ndec_sax_ctx_set_input */

  const NdecReactor *reactor;
  void *user_data;

  const uint8_t *cur_pos;
  const uint8_t *chunk_ptr;
  uint64_t structural_bits;

  NdecScanState scan_state;

  /* UTF-8 carry spans input segments and is reset only for a new document. */
  Utf8Checker utf8;

  uint32_t exit_code;
  uint32_t error_pos;

  int32_t sp;
  NdecFrame frames[NDEC_MAX_DEPTH];
} NdecSaxContext;

#endif /* !__NDEC_CORE_SAPI_H__ */
