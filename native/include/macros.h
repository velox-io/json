#ifndef __V_MACROS_H__
#define __V_MACROS_H__

#ifndef INLINE
#if defined(_MSC_VER)
#define INLINE __forceinline inline
#else
#define INLINE static __attribute__((always_inline)) inline
#endif
#endif

#ifndef NOINLINE
#if defined(_MSC_VER)
#define NOINLINE __declspec(noinline)
#else
#define NOINLINE __attribute__((noinline))
#endif
#endif

/* Alignment: ALIGNED_DECL(n) before declarator, ALIGNED(n) after. */
#if defined(_MSC_VER)
#define ALIGNED_DECL(n) __declspec(align(n))
#define ALIGNED(n)
#else
#define ALIGNED_DECL(n)
#define ALIGNED(n) __attribute__((aligned(n)))
#endif

/* Struct typedef alignment: ALIGN_TYPEDEF(n) before, ALIGN_TYPEDEF_END(n) after '}' */
#if defined(_MSC_VER)
#define ALIGN_TYPEDEF(n) __declspec(align(n))
#define ALIGN_TYPEDEF_END(n)
#else
#define ALIGN_TYPEDEF(n)
#define ALIGN_TYPEDEF_END(n) __attribute__((aligned(n)))
#endif

#if defined(_MSC_VER)
#define HIDDEN
#else
#define HIDDEN __attribute__((visibility("hidden")))
#endif

#if defined(_MSC_VER)
#define EXPORT __declspec(dllexport)
#else
#define EXPORT
#endif

/* force_align_arg_pointer: emit AND $-16,%rsp on x86-64 to fix
 * stack misalignment when called from Go ABI.  No-op elsewhere. */
#if defined(__x86_64__) && !defined(_WIN32)
#define ALIGN_STACK __attribute__((force_align_arg_pointer))
#else
#define ALIGN_STACK
#endif

#if defined(_MSC_VER)
#define NO_BUILTIN_FUNC(func)
#else
#define NO_BUILTIN_FUNC(func) __attribute__((no_builtin(#func)))
#endif

#if defined(_MSC_VER)
#define OPTNONE
#else
#define OPTNONE __attribute__((optnone))
#endif

#ifndef LIKELY
#define LIKELY(x) __builtin_expect(!!(x), 1)
#endif

#ifndef UNLIKELY
#define UNLIKELY(x) __builtin_expect(!!(x), 0)
#endif

#endif /* __V_MACROS_H__ */
