/*
 * Compilation entry point for the Go binding parser.
 */

#ifndef NDEC_FN_NAME

#if defined(__aarch64__)
#define NDEC_FN_NAME ndec_parse_default_neon
#elif defined(__x86_64__)
#define NDEC_FN_NAME ndec_parse_default_avx2
#else
#error "unsupported architecture"
#endif

#endif /* NDEC_FN_NAME */

#define NDEC_FN_DECL VJ_EXPORT VJ_ALIGN_STACK

// IWYU pragma: begin_keep
#include "vj_compat.h"
#include "bind_hooks.h"
#include "ndec/core/parser.h"
// IWYU pragma: end_keep
