//go:build darwin && arm64

#include "textflag.h"

// void pthread_jit_write_protect_np(int enabled)
//
// Tail jump into libSystem.
TEXT ·pthreadJitWriteProtect(SB), NOSPLIT, $0-4
	MOVW enabled+0(FP), R0
	JMP   _pthread_jit_write_protect_np(SB)
