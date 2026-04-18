/*
 * ndec_bench_parse.h — PARSE mode parser (full parse with inline callbacks)
 *
 * Reactor callbacks are resolved at compile time via NDEC_R_* macros.
 * This lets the compiler inline every callback body into the parser's hot path
 * and sink state (field_count, views[], etc.) can live in registers.
 */

#ifndef NDEC_BENCH_PARSE_H
#define NDEC_BENCH_PARSE_H

#undef NDEC_R_BEGIN_OBJECT
#undef NDEC_R_END_OBJECT
#undef NDEC_R_OBJECT_FIELD
#undef NDEC_R_BEGIN_ARRAY
#undef NDEC_R_END_ARRAY
#undef NDEC_R_SCALAR_NULL
#undef NDEC_R_SCALAR_BOOL
#undef NDEC_R_SCALAR_NUMBER
#undef NDEC_R_SCALAR_STRING

#define NDEC_R_BEGIN_OBJECT(ud)         r_begin_obj(ud)
#define NDEC_R_END_OBJECT(ud)           r_end_obj(ud)
#define NDEC_R_OBJECT_FIELD(ud, key)    r_field((ud), (key))
#define NDEC_R_BEGIN_ARRAY(ud)          r_begin_arr(ud)
#define NDEC_R_END_ARRAY(ud)            r_end_arr(ud)
#define NDEC_R_SCALAR_NULL(ud)          r_null(ud)
#define NDEC_R_SCALAR_BOOL(ud, v)       r_bool((ud), (v))
#define NDEC_R_SCALAR_NUMBER(ud, raw)   r_number((ud), (raw))
#define NDEC_R_SCALAR_STRING(ud, raw)   r_string((ud), (raw))

#define NDEC_FN_NAME ndec_parse_parse
#include "ndec/core/parser.h"
#undef NDEC_FN_NAME

#endif
