#ifndef VJ_COMPAT_H
#define VJ_COMPAT_H

/* Portable compiler-attribute macros for GCC/Clang/MSVC cross-compilation. */

#if defined(_MSC_VER)
#define INLINE __forceinline inline
#else
#define INLINE static __attribute__((always_inline)) inline
#endif

#if defined(_MSC_VER)
#define NOINLINE __declspec(noinline)
#else
#define NOINLINE __attribute__((noinline))
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
#define VJ_HIDDEN
#else
#define VJ_HIDDEN __attribute__((visibility("hidden")))
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

#endif /* VJ_COMPAT_H */
