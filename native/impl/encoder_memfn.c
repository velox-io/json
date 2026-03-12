/*
 * encoder_memfn.c — Standalone memcpy/memset for the native encoder .syso.
 *
 * Compiled once (no ISA flags) and linked alongside the ISA-specific
 * encoder objects, so that memcpy/memset symbols appear exactly once
 * in the final .syso.
 */

#define VJ_IMPLEMENT_MEMFN
#include "encoder_memory.h"
