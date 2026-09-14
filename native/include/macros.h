#ifndef __V_MACROS_H__
#define __V_MACROS_H__

#ifndef INLINE
#define INLINE static __attribute__((always_inline)) inline
#endif

#ifndef NOINLINE
#define NOINLINE __attribute__((noinline))
#endif

/* Alignment: ALIGNED_DECL(n) before declarator, ALIGNED(n) after. */
#define ALIGNED_DECL(n)
#define ALIGNED(n) __attribute__((aligned(n)))

/* Struct typedef alignment: ALIGN_TYPEDEF(n) before, ALIGN_TYPEDEF_END(n) after '}' */
#define ALIGN_TYPEDEF(n)
#define ALIGN_TYPEDEF_END(n) __attribute__((aligned(n)))

#define HIDDEN __attribute__((visibility("hidden")))

#define EXPORT

/* force_align_arg_pointer: emit AND $-16,%rsp on x86-64 to fix
 * stack misalignment when called from Go ABI.  No-op elsewhere. */
#if defined(__x86_64__) && !defined(_WIN32)
#define ALIGN_STACK __attribute__((force_align_arg_pointer))
#else
#define ALIGN_STACK
#endif

#define NO_BUILTIN_FUNC(func) __attribute__((no_builtin(#func)))

#define OPTNONE __attribute__((optnone))

/* Pin a local object to its stack slot, preventing SROA from promoting it
 * to SSA registers. The empty asm only makes the address observable to the
 * optimizer and emits no code. */
#define PIN_STACK_HOME(obj) __asm__ volatile("" : "+m"(obj))

#ifndef LIKELY
#define LIKELY(x) __builtin_expect(!!(x), 1)
#endif

#ifndef UNLIKELY
#define UNLIKELY(x) __builtin_expect(!!(x), 0)
#endif

#endif /* __V_MACROS_H__ */
