/*
 * ndec_bench_base.h — BASE mode parser (validate-only, zero reactor work)
 *
 * Pins every reactor hook to a compile-time no-op. This short-circuits the
 * default vtable dispatch so the branch+load cost vanishes and the compiler
 * can prove the callbacks are dead. The result is a pure validate path.
 */

#ifndef NDEC_BENCH_BASE_H
#define NDEC_BENCH_BASE_H

#undef NDEC_R_BEGIN_OBJECT
#undef NDEC_R_END_OBJECT
#undef NDEC_R_OBJECT_FIELD
#undef NDEC_R_BEGIN_ARRAY
#undef NDEC_R_END_ARRAY
#undef NDEC_R_ARRAY_ELEM
#undef NDEC_R_SCALAR_NULL
#undef NDEC_R_SCALAR_BOOL
#undef NDEC_R_SCALAR_NUMBER
#undef NDEC_R_SCALAR_STRING

#define NDEC_R_BEGIN_OBJECT(ud)         (NDEC_PROCEED)
#define NDEC_R_END_OBJECT(ud)           (NDEC_PROCEED)
#define NDEC_R_OBJECT_FIELD(ud, key)    (NDEC_PROCEED)
#define NDEC_R_BEGIN_ARRAY(ud)          (NDEC_PROCEED)
#define NDEC_R_END_ARRAY(ud)            (NDEC_PROCEED)
#define NDEC_R_ARRAY_ELEM(ud)           (NDEC_PROCEED)
#define NDEC_R_SCALAR_NULL(ud)          (NDEC_PROCEED)
#define NDEC_R_SCALAR_BOOL(ud, v)       (NDEC_PROCEED)
#define NDEC_R_SCALAR_NUMBER(ud, raw)   (NDEC_PROCEED)
#define NDEC_R_SCALAR_STRING(ud, raw)   (NDEC_PROCEED)

#define NDEC_FN_NAME ndec_parse_base
#include "ndec/core/parser.h"
#undef NDEC_FN_NAME
#undef NDEC_PARSER_H

#endif
