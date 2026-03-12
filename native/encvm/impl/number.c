#include "number.h"

NOINLINE int write_uint64_call(uint8_t *buf, uint64_t v) {
  return write_uint64(buf, v);
}

NOINLINE int write_int64_call(uint8_t *buf, uint64_t v) {
  return write_int64(buf, v);
}
