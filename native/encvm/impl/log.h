/*
 * Backward-compat shim: the implementation moved to native/util/log.{c,h}.
 * Kept so existing includes "log.h" from native/encvm/impl/ keep working
 * without touching every callsite.
 */
#ifndef VJ_ENCVM_LOG_H
#define VJ_ENCVM_LOG_H

#include "util/log.h"

#endif /* VJ_ENCVM_LOG_H */
