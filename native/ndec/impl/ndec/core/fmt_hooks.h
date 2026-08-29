/* fmt reactor hooks: static dispatch into the formatter emitters.
 * Included by sax.h via NDEC_REACTOR_HOOKS; fmt.h defines the emitters
 * before including sax.h, so every call site expands to a direct call
 * the compiler can inline. */

#ifndef NDEC_CORE_FMT_HOOKS_H
#define NDEC_CORE_FMT_HOOKS_H

#define NDEC_R_BEGIN_OBJECT(ud)       fmt_begin_object(ud)
#define NDEC_R_END_OBJECT(ud)         fmt_end_object(ud)
#define NDEC_R_OBJECT_FIELD(ud, key)  fmt_object_field((ud), (key))
#define NDEC_R_BEGIN_ARRAY(ud)        fmt_begin_array(ud)
#define NDEC_R_END_ARRAY(ud)          fmt_end_array(ud)
#define NDEC_R_SCALAR_NULL(ud)        fmt_scalar_null(ud)
#define NDEC_R_SCALAR_BOOL(ud, v)     fmt_scalar_bool((ud), (v))
#define NDEC_R_SCALAR_NUMBER(ud, raw) fmt_scalar_number((ud), (raw))
#define NDEC_R_SCALAR_STRING(ud, str) fmt_scalar_string((ud), (str))

#endif // !NDEC_CORE_FMT_HOOKS_H
