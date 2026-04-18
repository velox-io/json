#ifndef NDEC_HELPER_H
#define NDEC_HELPER_H

#ifndef INLINE
#if defined(_MSC_VER)
#define INLINE __forceinline inline
#else
#define INLINE static __attribute__((always_inline)) inline
#endif
#endif // !INLINE

#ifndef NOINLINE
#if defined(_MSC_VER)
#define NOINLINE __declspec(noinline)
#else
#define NOINLINE __attribute__((noinline))
#endif
#endif // !NOINLINE

#ifndef UNLIKELY
#define UNLIKELY(x) __builtin_expect(!!(x), 0)
#endif // !UNLIKELY

#ifndef LIKELY
#define LIKELY(x) __builtin_expect(!!(x), 1)
#endif // !LIKELY

#endif // !NDEC_HELPER_H
