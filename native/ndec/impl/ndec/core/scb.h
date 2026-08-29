/* SAX reactor default hook */

#ifndef __NDEC_CORE_SCB_H__
#define __NDEC_CORE_SCB_H__

#ifdef NDEC_USE_VTABLE
/* Runtime function-pointer dispatch through ctx->reactor. Used by tests
 * and casual embedders that prefer a vtable to compile-time hook wiring.
 * Each macro expands inside the parser body where `ctx` is the NdecSaxContext*
 * parameter; repeated ctx->reactor reads in a basic block get CSE'd. */
#define NDEC_R_BEGIN_OBJECT(ud)                                                                                   \
  ((ctx->reactor && ctx->reactor->begin_object) ? ctx->reactor->begin_object(ud) : NDEC_PROCEED)
#define NDEC_R_END_OBJECT(ud)                                                                                     \
  ((ctx->reactor && ctx->reactor->end_object) ? ctx->reactor->end_object(ud) : NDEC_PROCEED)
#define NDEC_R_OBJECT_FIELD(ud, key)                                                                              \
  ((ctx->reactor && ctx->reactor->object_field) ? ctx->reactor->object_field((ud), (key)) : NDEC_PROCEED)
#define NDEC_R_BEGIN_ARRAY(ud)                                                                                    \
  ((ctx->reactor && ctx->reactor->begin_array) ? ctx->reactor->begin_array(ud) : NDEC_PROCEED)
#define NDEC_R_END_ARRAY(ud)                                                                                      \
  ((ctx->reactor && ctx->reactor->end_array) ? ctx->reactor->end_array(ud) : NDEC_PROCEED)
#define NDEC_R_SCALAR_NULL(ud)                                                                                    \
  ((ctx->reactor && ctx->reactor->scalar_null) ? ctx->reactor->scalar_null(ud) : NDEC_PROCEED)
#define NDEC_R_SCALAR_BOOL(ud, v)                                                                                 \
  ((ctx->reactor && ctx->reactor->scalar_bool) ? ctx->reactor->scalar_bool((ud), (v)) : NDEC_PROCEED)
#define NDEC_R_SCALAR_NUMBER(ud, raw)                                                                             \
  ((ctx->reactor && ctx->reactor->scalar_number) ? ctx->reactor->scalar_number((ud), (raw)) : NDEC_PROCEED)
#define NDEC_R_SCALAR_STRING(ud, raw)                                                                             \
  ((ctx->reactor && ctx->reactor->scalar_string) ? ctx->reactor->scalar_string((ud), (raw)) : NDEC_PROCEED)
#endif /* NDEC_USE_VTABLE */

/* Callback dispatch macros. Defaults are no ops; hosts override before
 * including this header (see sax.h for the optional NDEC_USE_VTABLE
 * runtime-dispatch facade). */
#ifndef NDEC_R_BEGIN_OBJECT
#define NDEC_R_BEGIN_OBJECT(ud) (NDEC_PROCEED)
#endif
#ifndef NDEC_R_END_OBJECT
#define NDEC_R_END_OBJECT(ud) (NDEC_PROCEED)
#endif
#ifndef NDEC_R_OBJECT_FIELD
#define NDEC_R_OBJECT_FIELD(ud, key) (NDEC_PROCEED)
#endif
#ifndef NDEC_R_BEGIN_ARRAY
#define NDEC_R_BEGIN_ARRAY(ud) (NDEC_PROCEED)
#endif
#ifndef NDEC_R_END_ARRAY
#define NDEC_R_END_ARRAY(ud) (NDEC_PROCEED)
#endif
#ifndef NDEC_R_SCALAR_NULL
#define NDEC_R_SCALAR_NULL(ud) (NDEC_PROCEED)
#endif
#ifndef NDEC_R_SCALAR_BOOL
#define NDEC_R_SCALAR_BOOL(ud, v) (NDEC_PROCEED)
#endif
#ifndef NDEC_R_SCALAR_NUMBER
#define NDEC_R_SCALAR_NUMBER(ud, raw) (NDEC_PROCEED)
#endif
#ifndef NDEC_R_SCALAR_STRING
#define NDEC_R_SCALAR_STRING(ud, raw) (NDEC_PROCEED)
#endif

/* Container-specialized scalar macros. */
#ifndef NDEC_R_OBJ_SCALAR_NULL
#define NDEC_R_OBJ_SCALAR_NULL(ud) NDEC_R_SCALAR_NULL(ud)
#endif
#ifndef NDEC_R_OBJ_SCALAR_BOOL
#define NDEC_R_OBJ_SCALAR_BOOL(ud, v) NDEC_R_SCALAR_BOOL((ud), (v))
#endif
#ifndef NDEC_R_OBJ_SCALAR_NUMBER
#define NDEC_R_OBJ_SCALAR_NUMBER(ud, raw) NDEC_R_SCALAR_NUMBER((ud), (raw))
#endif
#ifndef NDEC_R_OBJ_SCALAR_STRING
#define NDEC_R_OBJ_SCALAR_STRING(ud, raw) NDEC_R_SCALAR_STRING((ud), (raw))
#endif

#ifndef NDEC_R_ARR_SCALAR_NULL
#define NDEC_R_ARR_SCALAR_NULL(ud) NDEC_R_SCALAR_NULL(ud)
#endif
#ifndef NDEC_R_ARR_SCALAR_BOOL
#define NDEC_R_ARR_SCALAR_BOOL(ud, v) NDEC_R_SCALAR_BOOL((ud), (v))
#endif
#ifndef NDEC_R_ARR_SCALAR_NUMBER
#define NDEC_R_ARR_SCALAR_NUMBER(ud, raw) NDEC_R_SCALAR_NUMBER((ud), (raw))
#endif
#ifndef NDEC_R_ARR_SCALAR_STRING
#define NDEC_R_ARR_SCALAR_STRING(ud, raw) NDEC_R_SCALAR_STRING((ud), (raw))
#endif

/*
 *  Root-specialized scalar macros.
 *
 *  Top-level non-container values ("null" / 42 / "hi" / true / false)
 *  are fundamentally different from OBJECT field values or ARRAY
 *  elements: there is no parent frame, no field index, no array slot.
 *  Embedders that want to bind a root scalar to a host-side target
 *  need a separate hook that knows the root frame layout.
 *
 *  Default: forward to the generic NDEC_R_SCALAR_* form. Embedders
 *  that want a root specialization override these four macros to
 *  point at their own inline hooks, writing through the root frame
 *  directly without touching the OBJECT / ARRAY paths.
 */
#ifndef NDEC_R_ROOT_SCALAR_NULL
#define NDEC_R_ROOT_SCALAR_NULL(ud) NDEC_R_SCALAR_NULL(ud)
#endif
#ifndef NDEC_R_ROOT_SCALAR_BOOL
#define NDEC_R_ROOT_SCALAR_BOOL(ud, v) NDEC_R_SCALAR_BOOL((ud), (v))
#endif
#ifndef NDEC_R_ROOT_SCALAR_NUMBER
#define NDEC_R_ROOT_SCALAR_NUMBER(ud, raw) NDEC_R_SCALAR_NUMBER((ud), (raw))
#endif
#ifndef NDEC_R_ROOT_SCALAR_STRING
#define NDEC_R_ROOT_SCALAR_STRING(ud, raw) NDEC_R_SCALAR_STRING((ud), (raw))
#endif

/*
 * Container-open hooks specialized by parent context.
 *
 * Three variants per type, mirroring the OBJ_SCALAR / ARR_SCALAR /
 * ROOT_SCALAR split: the parser knows from the dispatch site whether
 * the new container is starting under the root sentinel, an object
 * field, or an array element. Hosts that need to bump the parent
 * array's child count (e.g. building a DOM) override the ARR_BEGIN_*
 * variants only, eliminating a runtime parent-type check on every
 * begin.
 *
 * Default: forward to the generic NDEC_R_BEGIN_* form so existing
 * hosts keep working unchanged.
 */
#ifndef NDEC_R_ROOT_BEGIN_OBJECT
#define NDEC_R_ROOT_BEGIN_OBJECT(ud) NDEC_R_BEGIN_OBJECT(ud)
#endif
#ifndef NDEC_R_ROOT_BEGIN_ARRAY
#define NDEC_R_ROOT_BEGIN_ARRAY(ud) NDEC_R_BEGIN_ARRAY(ud)
#endif
#ifndef NDEC_R_OBJ_BEGIN_OBJECT
#define NDEC_R_OBJ_BEGIN_OBJECT(ud) NDEC_R_BEGIN_OBJECT(ud)
#endif
#ifndef NDEC_R_OBJ_BEGIN_ARRAY
#define NDEC_R_OBJ_BEGIN_ARRAY(ud) NDEC_R_BEGIN_ARRAY(ud)
#endif
#ifndef NDEC_R_ARR_BEGIN_OBJECT
#define NDEC_R_ARR_BEGIN_OBJECT(ud) NDEC_R_BEGIN_OBJECT(ud)
#endif
#ifndef NDEC_R_ARR_BEGIN_ARRAY
#define NDEC_R_ARR_BEGIN_ARRAY(ud) NDEC_R_BEGIN_ARRAY(ud)
#endif
#endif // !__NDEC_CORE_SCB_H__
