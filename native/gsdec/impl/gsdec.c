/*
 * gsdec.c — Goto Streaming Decoder.
 *
 * Single-function JSON parser using:
 * - Direct goto for forward flow (enter containers, next field)
 * - Computed goto for backward flow (close containers, resume)
 * - Explicit stack replacing goroutine recursion
 * - Rollback on buffer exhaustion (retry after refill)
 *
 * Phase 1: Flat struct with string/number/bool/null + streaming.
 *          Nested struct via stack push/pop + computed goto.
 */

#include "gsdec.h"
#include "scanner.h"
#include "fpparse.h"

/* ============================================================
 *  Writer helpers — write decoded values to Go memory
 * ============================================================ */

static inline void write_int(uint8_t *target, uint8_t kind, int64_t val) {
    switch (kind) {
        case KIND_INT:   *(int64_t*)target = val; break;
        case KIND_INT8:  *(int8_t*)target  = (int8_t)val; break;
        case KIND_INT16: *(int16_t*)target = (int16_t)val; break;
        case KIND_INT32: *(int32_t*)target = (int32_t)val; break;
        case KIND_INT64: *(int64_t*)target = val; break;
    }
}

static inline void write_uint(uint8_t *target, uint8_t kind, uint64_t val) {
    switch (kind) {
        case KIND_UINT:   *(uint64_t*)target = val; break;
        case KIND_UINT8:  *(uint8_t*)target  = (uint8_t)val; break;
        case KIND_UINT16: *(uint16_t*)target = (uint16_t)val; break;
        case KIND_UINT32: *(uint32_t*)target = (uint32_t)val; break;
        case KIND_UINT64: *(uint64_t*)target = val; break;
    }
}

static inline void write_string(uint8_t *target, const uint8_t *ptr, uint64_t len) {
    /* Go string header: [ptr:uintptr, len:uintptr] */
    *(uintptr_t*)target = (uintptr_t)ptr;
    *((uintptr_t*)target + 1) = (uintptr_t)len;
}

static inline void write_bool(uint8_t *target, uint8_t val) {
    *target = val;
}

/* ============================================================
 *  Field lookup in struct descriptor
 * ============================================================ */

static inline const DecFieldDesc *find_field(const DecStructDesc *desc,
                                              const uint8_t *key, uint32_t key_len) {
    const DecFieldDesc *fields = (const DecFieldDesc *)((const uint8_t *)desc + 16);
    uint16_t n = desc->num_fields;
    for (uint16_t i = 0; i < n; i++) {
        const uint8_t *fname = (const uint8_t *)fields[i].name_ptr;
        uint16_t flen = fields[i].name_len;
        if (bytes_equal(fname, flen, key, key_len)) {
            return &fields[i];
        }
    }
    return 0; /* not found */
}

/* ============================================================
 *  Main parser entry point
 * ============================================================ */

void vj_gdec_exec(DecExecCtx *ctx) {
    const uint8_t *src = ctx->src_ptr;
    uint32_t src_len   = ctx->src_len;
    uint32_t idx       = ctx->idx;
    uint8_t  c;

    /* Explicit parse stack — read from ctx->resume_ptr (Go-allocated) */
    GsdecFrame *stack = (GsdecFrame *)ctx->resume_ptr;
    uint16_t depth = ctx->resume_depth;

    /* Local variables for inline value parsing */
    uint8_t       *target;
    uint8_t        val_kind;
    const uint8_t *val_desc;
    uint32_t       entry_start;
    uint32_t       num_start, str_start, str_end;
    int            has_escape;

    /* ---- PIC-safe dispatch table (int32 offsets from base label) ---- */
    static const int32_t dispatch_table[ST_COUNT] = {
        [ST_OBJECT_BODY]     = GDT_ENTRY(L_object_body),
        [ST_OBJECT_NEXT]     = GDT_ENTRY(L_object_next),
        [ST_SLICE_BODY]      = GDT_ENTRY(L_slice_body),
        [ST_SLICE_NEXT]      = GDT_ENTRY(L_slice_next),
        [ST_ARRAY_BODY]      = GDT_ENTRY(L_array_body),
        [ST_ARRAY_NEXT]      = GDT_ENTRY(L_array_next),
        [ST_MAP_BODY]        = GDT_ENTRY(L_map_body),
        [ST_MAP_NEXT]        = GDT_ENTRY(L_map_next),
        [ST_SKIP_OBJECT]     = GDT_ENTRY(L_skip_object),
        [ST_SKIP_ARRAY]      = GDT_ENTRY(L_skip_array),
        [ST_ANY_OBJECT_BODY] = GDT_ENTRY(L_any_object_body),
        [ST_ANY_ARRAY_BODY]  = GDT_ENTRY(L_any_array_body),
        [ST_ROOT]            = GDT_ENTRY(L_root),
    };

    /* ---- Resume or initial entry ---- */
    if (depth > 0) {
        /* Resume: jump to where we left off */
        DISPATCH_TOP();
    }

    /* First dispatch + base label for PIC offset calculation */
    goto L_initial_entry;
gsdec_dispatch_base:
    __builtin_unreachable();

L_initial_entry:

    /* First call: push root and start parsing */
    target   = ctx->cur_base;
    val_kind = *ctx->ti_ptr;
    val_desc = ctx->ti_ptr;
    goto L_parse_value;

/* ==============================================================
 *  L_root — top-level resume target (after buffer refill)
 * ============================================================== */
L_root:
    target   = stack[depth - 1].base;
    val_kind = stack[depth - 1].val_kind;
    val_desc = stack[depth - 1].desc;
    STACK_POP();
    /* fall through to L_parse_value */

/* ==============================================================
 *  L_parse_value — dispatch on first byte of a JSON value.
 *  Inline primitives; push stack for containers.
 *  target/val_kind/val_desc must be set by caller.
 * ============================================================== */
L_parse_value:
    SKIP_WS();
    if (idx >= src_len) {
        /* Push root frame so resume returns here */
        stack[depth].state = ST_ROOT;
        stack[depth].base = target;
        stack[depth].val_kind = val_kind;
        stack[depth].desc = val_desc;
        depth++;
        SUSPEND_EOF(idx);
    }

    /* ---- Pointer: dereference, allocate if nil ---- */
    if (val_kind == KIND_POINTER) {
        const DecPointerDesc *pd = (const DecPointerDesc *)val_desc;
        /* Check for null */
        if (src[idx] == 'n') {
            if (idx + 4 > src_len) {
                stack[depth].state = ST_ROOT;
                stack[depth].base = target;
                stack[depth].val_kind = val_kind;
                stack[depth].desc = val_desc;
                depth++;
                SUSPEND_EOF(idx);
            }
            idx += 4;
            /* Set pointer to nil */
            *(uintptr_t *)target = 0;
            goto L_value_done;
        }
        /* Read current pointer value */
        uint8_t *elem_ptr = *(uint8_t **)target;
        if (elem_ptr) {
            /* Non-nil: reuse existing allocation */
            target = elem_ptr;
            val_kind = pd->elem_kind;
            val_desc = (const uint8_t *)pd->elem_desc;
            goto L_parse_value;
        }
        /* Nil: yield to Go for allocation.
         * Don't push any frame — let the parent handle rollback.
         * After Go allocates, the parent re-parses this field.
         * On re-entry, pointer is non-nil, takes reuse path above. */
        ctx->idx = idx;
        ctx->yield_param0 = (uint64_t)(uintptr_t)pd->elem_rtype;
        ctx->yield_param1 = (uint64_t)(uintptr_t)target;
        ctx->yield_param2 = (uint64_t)pd->elem_has_ptr;
        SUSPEND_YIELD(YIELD_ALLOC_POINTER);
    }

    /* ---- Any/interface{}: emit insn instructions ---- */
    if (val_kind == KIND_ANY) {
        /* Don't emit SET_TARGET here — do it in L_parse_any_value AFTER
         * the EOF check, so insn isn't polluted if we suspend. */
        goto L_parse_any_value;
    }

    switch (src[idx]) {

    /* ---- Object ---- */
    case '{':
        idx++;
        if (val_kind == KIND_STRUCT) {
            stack[depth].state = ST_OBJECT_BODY;
            stack[depth].base  = target;
            stack[depth].desc  = val_desc;
            stack[depth].aux   = 0;
            depth++;
            goto L_object_body;
        }
        if (val_kind == KIND_MAP) {
            /* Check empty */
            SKIP_WS();
            if (idx >= src_len) {
                stack[depth].state = ST_MAP_BODY;
                stack[depth].base  = target;
                stack[depth].desc  = val_desc;
                stack[depth].aux   = 0;
                stack[depth].aux2  = 0xFFFFFFFF; /* sentinel: needs init */
                depth++;
                SUSPEND_EOF(idx);
            }
            {
                const DecMapDesc *md = (const DecMapDesc *)val_desc;
                int is_empty = (src[idx] == '}');
                if (is_empty) idx++;
                ctx->idx = idx;
                ctx->yield_param0 = (uint64_t)(uintptr_t)md->codec_ptr;
                ctx->yield_param1 = (uint64_t)(uintptr_t)target;
                ctx->yield_param2 = is_empty ? 1 : 0;
                stack[depth].state = ST_MAP_BODY;
                stack[depth].base  = target;
                stack[depth].desc  = val_desc;
                stack[depth].aux   = 0;
                stack[depth].aux2  = 0; /* read buf_cap from yield_param1 on resume */
                depth++;
                SUSPEND_YIELD(YIELD_MAP_INIT);
            }
        }
        /* Type mismatch: skip the object */
        stack[depth].state = ST_SKIP_OBJECT;
        stack[depth].base  = 0;
        stack[depth].desc  = 0;
        stack[depth].aux   = 0;
        depth++;
        goto L_skip_object;

    /* ---- Array ---- */
    case '[':
        idx++;
        if (val_kind == KIND_SLICE) {
            /* Check for empty slice */
            SKIP_WS();
            if (idx >= src_len) {
                /* Need more data to tell if empty. Push SliceBody with count=0
                 * but we haven't yielded for alloc yet. Use aux2=0xFFFFFFFF
                 * as sentinel to indicate "not yet allocated". */
                stack[depth].state = ST_SLICE_BODY;
                stack[depth].base  = target;
                stack[depth].desc  = val_desc;
                stack[depth].aux   = 0;
                stack[depth].aux2  = 0xFFFFFFFF; /* sentinel: needs init */
                depth++;
                SUSPEND_EOF(idx);
            }
            if (src[idx] == ']') {
                idx++;
                /* Empty slice: write zero SliceHeader (24 bytes) */
                if (target) {
                    for (int i = 0; i < 24; i++) target[i] = 0;
                }
                goto L_value_done;
            }
            /* Non-empty: yield ALLOC_SLICE */
            {
                const DecSliceDesc *sd = (const DecSliceDesc *)val_desc;
                ctx->idx = idx;
                ctx->yield_param0 = (uint64_t)(uintptr_t)sd->codec_ptr;
                ctx->yield_param1 = (uint64_t)(uintptr_t)target;
                ctx->yield_param2 = (uint64_t)(uintptr_t)sd->elem_rtype;
                stack[depth].state = ST_SLICE_BODY;
                stack[depth].base  = target;
                stack[depth].desc  = val_desc;
                stack[depth].aux   = 0;  /* count = 0 */
                stack[depth].aux2  = 0;  /* cap = 0 → read from yield_param1 on resume */
                depth++;
                SUSPEND_YIELD(YIELD_ALLOC_SLICE);
            }
        }
        if (val_kind == KIND_ARRAY) {
            /* Fixed-length array */
            const DecArrayDesc *ad = (const DecArrayDesc *)val_desc;
            stack[depth].state = ST_ARRAY_BODY;
            stack[depth].base  = target;
            stack[depth].desc  = val_desc;
            stack[depth].aux   = 0;  /* count = 0 */
            stack[depth].aux2  = ad->array_len;
            depth++;
            goto L_array_body;
        }
        /* Type mismatch: skip */
        stack[depth].state = ST_SKIP_ARRAY;
        stack[depth].base  = 0;
        stack[depth].desc  = 0;
        stack[depth].aux   = 0;
        depth++;
        goto L_skip_array;

    /* ---- String ---- */
    case '"':
        idx++;
        if (scan_string(src, src_len, &idx, &str_start, &str_end, &has_escape)) {
            /* EOF mid-string: rollback to opening " */
            SUSPEND_EOF(str_start - 1);
        }
        if (target && val_kind == KIND_STRING) {
            if (!has_escape) {
                write_string(target, src + str_start, str_end - str_start);
            } else {
                /* Unescape into scratch arena */
                /* TODO: implement arena unescape */
                write_string(target, src + str_start, str_end - str_start);
            }
        }
        goto L_value_done;

    /* ---- Number ---- */
    case '-': case '0': case '1': case '2': case '3': case '4':
    case '5': case '6': case '7': case '8': case '9':
        num_start = idx;
        if (scan_number(src, src_len, &idx)) {
            /* EOF mid-number */
            SUSPEND_EOF(num_start);
        }
        if (target) {
            if (kind_is_signed_int(val_kind)) {
                write_int(target, val_kind, parse_int64(src, num_start, idx));
            } else if (kind_is_unsigned_int(val_kind)) {
                write_uint(target, val_kind, parse_uint64(src, num_start, idx));
            }
            else if (val_kind == KIND_FLOAT64) {
                *(double*)target = parse_float64(src, num_start, idx);
            } else if (val_kind == KIND_FLOAT32) {
                *(float*)target = (float)parse_float64(src, num_start, idx);
            }
        }
        goto L_value_done;

    /* ---- true ---- */
    case 't':
        if (idx + 4 > src_len) SUSPEND_EOF(idx);
        idx += 4;
        if (target && val_kind == KIND_BOOL) write_bool(target, 1);
        goto L_value_done;

    /* ---- false ---- */
    case 'f':
        if (idx + 5 > src_len) SUSPEND_EOF(idx);
        idx += 5;
        if (target && val_kind == KIND_BOOL) write_bool(target, 0);
        goto L_value_done;

    /* ---- null ---- */
    case 'n':
        if (idx + 4 > src_len) SUSPEND_EOF(idx);
        idx += 4;
        goto L_value_done;

    default:
        ERROR_AT(idx, EXIT_SYNTAX_ERROR);
    }

/* ==============================================================
 *  L_value_done — a value was fully parsed.
 *  Return to parent via computed goto.
 * ============================================================== */
L_value_done:
    ctx->idx = idx;
    if (depth == 0) {
        ctx->exit_code = EXIT_OK;
        return;
    }
    DISPATCH_TOP();

/* ==============================================================
 *  L_object_body — Parse struct fields: "key": value, ...
 *  Inner loop: direct goto between fields, no dispatch overhead.
 * ============================================================== */
L_object_body:
    stack[depth - 1].state = ST_OBJECT_BODY; /* ensure correct resume state */
    SKIP_WS();
    if (idx >= src_len) {
        SUSPEND_EOF(idx);
    }
    if (src[idx] == '}') {
        idx++;
        STACK_POP();
        goto L_value_done;
    }
    if (src[idx] == ',') {
        idx++;
        SKIP_WS();
    }
    /* fall through to parse key */

    {
        const DecStructDesc *sdesc = (const DecStructDesc *)stack[depth - 1].desc;
        uint8_t *base = stack[depth - 1].base;
        entry_start = idx;

        /* Expect opening " */
        if (idx >= src_len) SUSPEND_EOF(entry_start);
        if (src[idx] != '"') ERROR_AT(idx, EXIT_SYNTAX_ERROR);
        idx++;

        /* Scan key */
        uint32_t key_start, key_end;
        int key_esc;
        if (scan_string(src, src_len, &idx, &key_start, &key_end, &key_esc)) {
            SUSPEND_EOF(entry_start);
        }

        /* Expect colon */
        SKIP_WS();
        if (idx >= src_len) SUSPEND_EOF(entry_start);
        if (src[idx] != ':') ERROR_AT(idx, EXIT_SYNTAX_ERROR);
        idx++;
        SKIP_WS();

        /* Lookup field */
        const DecFieldDesc *field = find_field(sdesc, src + key_start, key_end - key_start);
        if (field) {
            target   = base + field->offset;
            val_kind = field->val_kind;
            val_desc = (const uint8_t *)field->val_desc;

            /* Pointer: unwrap before parsing value */
            if (val_kind == KIND_POINTER) {
                if (idx >= src_len) SUSPEND_EOF(entry_start);
                if (src[idx] == 'n') {
                    /* null → nil pointer */
                    if (idx + 4 > src_len) SUSPEND_EOF(entry_start);
                    idx += 4;
                    *(uintptr_t *)target = 0;
                    stack[depth - 1].state = ST_OBJECT_NEXT;
                    goto L_object_next;
                }
                const DecPointerDesc *pd2 = (const DecPointerDesc *)val_desc;
                uint8_t *elem_ptr2 = *(uint8_t **)target;
                if (elem_ptr2) {
                    /* Reuse existing allocation */
                    target = elem_ptr2;
                    val_kind = pd2->elem_kind;
                    val_desc = (const uint8_t *)pd2->elem_desc;
                } else {
                    /* Nil: yield alloc. Rollback to entry_start so object body
                     * re-parses the key on resume. Pointer is now non-nil → reuse. */
                    ctx->yield_param0 = (uint64_t)(uintptr_t)pd2->elem_rtype;
                    ctx->yield_param1 = (uint64_t)(uintptr_t)target;
                    ctx->yield_param2 = (uint64_t)pd2->elem_has_ptr;
                    ctx->exit_code = YIELD_ALLOC_POINTER;
                    ctx->idx = entry_start;
                    ctx->resume_depth = depth;
                    return;
                }
            }

            /* Any/interface{}: route through insn mode */
            if (val_kind == KIND_ANY) {
                if (idx >= src_len) SUSPEND_EOF(entry_start);
                stack[depth - 1].state = ST_OBJECT_NEXT;
                /* SET_TARGET is emitted inside L_parse_any_value after EOF check */
                goto L_parse_any_value;
            }

            /* NOTE: Do NOT set state=ST_OBJECT_NEXT here! */

            /* Inline primitives — direct goto, no stack push */
            if (idx >= src_len) SUSPEND_EOF(entry_start);
            switch (src[idx]) {
            case '"':
                idx++;
                if (scan_string(src, src_len, &idx, &str_start, &str_end, &has_escape)) {
                    SUSPEND_EOF(entry_start);
                }
                if (val_kind == KIND_STRING) {
                    if (!has_escape) {
                        write_string(target, src + str_start, str_end - str_start);
                    } else {
                        /* Unescape into scratch arena */
                        uint32_t raw_len = str_end - str_start;
                        uint32_t arena_used = ctx->scratch_len;
                        uint32_t arena_cap = ctx->scratch_cap;
                        if (arena_used + raw_len > arena_cap) {
                            ctx->yield_param0 = (uint64_t)raw_len;
                            ctx->exit_code = YIELD_ARENA_FULL;
                            ctx->idx = entry_start;
                            ctx->resume_depth = depth;
                            return;
                        }
                        uint8_t *arena_dst = ctx->scratch_ptr + arena_used;
                        uint32_t written = unescape_to(src + str_start, raw_len, arena_dst, arena_cap - arena_used);
                        write_string(target, arena_dst, written);
                        ctx->scratch_len = arena_used + written;
                    }
                }
                stack[depth - 1].state = ST_OBJECT_NEXT;
                goto L_object_next;

            case '-': case '0': case '1': case '2': case '3': case '4':
            case '5': case '6': case '7': case '8': case '9':
                num_start = idx;
                if (scan_number(src, src_len, &idx)) {
                    SUSPEND_EOF(entry_start);
                }
                if (kind_is_signed_int(val_kind)) {
                    write_int(target, val_kind, parse_int64(src, num_start, idx));
                } else if (kind_is_unsigned_int(val_kind)) {
                    write_uint(target, val_kind, parse_uint64(src, num_start, idx));
                } else if (val_kind == KIND_FLOAT64) {
                    *(double*)target = parse_float64(src, num_start, idx);
                } else if (val_kind == KIND_FLOAT32) {
                    *(float*)target = (float)parse_float64(src, num_start, idx);
                }
                stack[depth - 1].state = ST_OBJECT_NEXT;
                goto L_object_next;

            case 't':
                if (idx + 4 > src_len) SUSPEND_EOF(entry_start);
                idx += 4;
                if (val_kind == KIND_BOOL) write_bool(target, 1);
                stack[depth - 1].state = ST_OBJECT_NEXT;
                goto L_object_next;

            case 'f':
                if (idx + 5 > src_len) SUSPEND_EOF(entry_start);
                idx += 5;
                if (val_kind == KIND_BOOL) write_bool(target, 0);
                stack[depth - 1].state = ST_OBJECT_NEXT;
                goto L_object_next;

            case 'n':
                if (idx + 4 > src_len) SUSPEND_EOF(entry_start);
                idx += 4;
                stack[depth - 1].state = ST_OBJECT_NEXT;
                goto L_object_next;

            case '{':
                /* Nested container: push child, direct goto */
                idx++;
                if (val_kind == KIND_STRUCT) {
                    stack[depth - 1].state = ST_OBJECT_NEXT;
                    stack[depth].state = ST_OBJECT_BODY;
                    stack[depth].base  = target;
                    stack[depth].desc  = val_desc;
                    stack[depth].aux   = 0;
                    depth++;
                    goto L_object_body;
                }
                if (val_kind == KIND_MAP) {
                    /* Check empty + yield MAP_INIT */
                    SKIP_WS();
                    if (idx >= src_len) SUSPEND_EOF(entry_start);
                    stack[depth - 1].state = ST_OBJECT_NEXT;
                    {
                        const DecMapDesc *md2 = (const DecMapDesc *)val_desc;
                        int is_empty = (src[idx] == '}');
                        if (is_empty) idx++;
                        ctx->idx = idx;
                        ctx->yield_param0 = (uint64_t)(uintptr_t)md2->codec_ptr;
                        ctx->yield_param1 = (uint64_t)(uintptr_t)target;
                        ctx->yield_param2 = is_empty ? 1 : 0;
                        stack[depth].state = ST_MAP_BODY;
                        stack[depth].base  = target;
                        stack[depth].desc  = val_desc;
                        stack[depth].aux   = 0;
                        stack[depth].aux2  = 0;
                        depth++;
                        SUSPEND_YIELD(YIELD_MAP_INIT);
                    }
                }
                /* Type mismatch: skip */
                stack[depth].state = ST_SKIP_OBJECT;
                stack[depth].base  = 0;
                stack[depth].desc  = 0;
                stack[depth].aux   = 0;
                depth++;
                goto L_skip_object;

            case '[':
                idx++;
                if (val_kind == KIND_SLICE) {
                    /* Check empty */
                    SKIP_WS();
                    if (idx >= src_len) SUSPEND_EOF(entry_start);
                    if (src[idx] == ']') {
                        idx++;
                        if (target) { for (int i = 0; i < 24; i++) target[i] = 0; }
                        stack[depth - 1].state = ST_OBJECT_NEXT;
                        goto L_object_next;
                    }
                    /* Yield for alloc */
                    stack[depth - 1].state = ST_OBJECT_NEXT;
                    {
                        const DecSliceDesc *sd = (const DecSliceDesc *)val_desc;
                        ctx->idx = idx;
                        ctx->yield_param0 = (uint64_t)(uintptr_t)sd->codec_ptr;
                        ctx->yield_param1 = (uint64_t)(uintptr_t)target;
                        ctx->yield_param2 = (uint64_t)(uintptr_t)sd->elem_rtype;
                        stack[depth].state = ST_SLICE_BODY;
                        stack[depth].base  = target;
                        stack[depth].desc  = val_desc;
                        stack[depth].aux   = 0;
                        stack[depth].aux2  = 0;
                        depth++;
                        SUSPEND_YIELD(YIELD_ALLOC_SLICE);
                    }
                }
                if (val_kind == KIND_ARRAY) {
                    stack[depth - 1].state = ST_OBJECT_NEXT;
                    const DecArrayDesc *ad = (const DecArrayDesc *)val_desc;
                    stack[depth].state = ST_ARRAY_BODY;
                    stack[depth].base  = target;
                    stack[depth].desc  = val_desc;
                    stack[depth].aux   = 0;
                    stack[depth].aux2  = ad->array_len;
                    depth++;
                    goto L_array_body;
                }
                /* Type mismatch: skip */
                stack[depth - 1].state = ST_OBJECT_NEXT;
                stack[depth].state = ST_SKIP_ARRAY;
                stack[depth].base  = 0;
                stack[depth].desc  = 0;
                stack[depth].aux   = 0;
                depth++;
                goto L_skip_array;

            default:
                ERROR_AT(idx, EXIT_SYNTAX_ERROR);
            }
        } else {
            /* Unknown field: skip value */
            if (idx >= src_len) SUSPEND_EOF(entry_start);
            switch (src[idx]) {
            case '{':
                idx++;
                stack[depth - 1].state = ST_OBJECT_NEXT;
                stack[depth].state = ST_SKIP_OBJECT;
                stack[depth].base  = 0;
                stack[depth].desc  = 0;
                stack[depth].aux   = 0;
                depth++;
                goto L_skip_object;
            case '[':
                idx++;
                stack[depth - 1].state = ST_OBJECT_NEXT;
                stack[depth].state = ST_SKIP_ARRAY;
                stack[depth].base  = 0;
                stack[depth].desc  = 0;
                stack[depth].aux   = 0;
                depth++;
                goto L_skip_array;
            case '"':
                /* Skip string inline */
                idx++;
                if (scan_string(src, src_len, &idx, &str_start, &str_end, &has_escape)) {
                    SUSPEND_EOF(entry_start);
                }
                stack[depth - 1].state = ST_OBJECT_NEXT;
                goto L_object_next;
            case '-': case '0': case '1': case '2': case '3': case '4':
            case '5': case '6': case '7': case '8': case '9':
                num_start = idx;
                if (scan_number(src, src_len, &idx)) {
                    SUSPEND_EOF(entry_start);
                }
                stack[depth - 1].state = ST_OBJECT_NEXT;
                goto L_object_next;
            case 't':
                if (idx + 4 > src_len) SUSPEND_EOF(entry_start);
                idx += 4;
                stack[depth - 1].state = ST_OBJECT_NEXT;
                goto L_object_next;
            case 'f':
                if (idx + 5 > src_len) SUSPEND_EOF(entry_start);
                idx += 5;
                stack[depth - 1].state = ST_OBJECT_NEXT;
                goto L_object_next;
            case 'n':
                if (idx + 4 > src_len) SUSPEND_EOF(entry_start);
                idx += 4;
                stack[depth - 1].state = ST_OBJECT_NEXT;
                goto L_object_next;
            default:
                ERROR_AT(idx, EXIT_SYNTAX_ERROR);
            }
        }
    }

/* ==============================================================
 *  L_object_next — After a field value, check , or }
 * ============================================================== */
L_object_next:
    stack[depth - 1].state = ST_OBJECT_NEXT;
    SKIP_WS();
    if (idx >= src_len) {
        SUSPEND_EOF(idx);
    }
    if (src[idx] == '}') {
        idx++;
        STACK_POP();
        goto L_value_done;
    }
    if (src[idx] == ',') {
        idx++;
        goto L_object_body;  /* direct goto: next field */
    }
    ERROR_AT(idx, EXIT_SYNTAX_ERROR);

/* ==============================================================
 *  L_skip_object — Skip an entire JSON object (streaming)
 * ============================================================== */
L_skip_object:
    stack[depth - 1].state = ST_SKIP_OBJECT;
    SKIP_WS();
    if (idx >= src_len) {
        SUSPEND_EOF(idx);
    }
    if (src[idx] == '}') {
        idx++;
        STACK_POP();
        goto L_value_done;
    }
    if (src[idx] == ',') {
        idx++;
        SKIP_WS();
    }
    {
        uint32_t skip_entry = idx;

        /* Key */
        if (idx >= src_len) SUSPEND_EOF(skip_entry);
        if (src[idx] != '"') ERROR_AT(idx, EXIT_SYNTAX_ERROR);
        idx++;
        uint32_t ks, ke;
        int kesc;
        if (scan_string(src, src_len, &idx, &ks, &ke, &kesc)) {
            SUSPEND_EOF(skip_entry);
        }

        /* Colon */
        SKIP_WS();
        if (idx >= src_len) SUSPEND_EOF(skip_entry);
        if (src[idx] != ':') ERROR_AT(idx, EXIT_SYNTAX_ERROR);
        idx++;
        SKIP_WS();

        /* Value */
        if (idx >= src_len) SUSPEND_EOF(skip_entry);
        switch (src[idx]) {
        case '{':
            idx++;
            stack[depth - 1].state = ST_SKIP_OBJECT; /* return to skip_object after nested */
            stack[depth].state = ST_SKIP_OBJECT;
            stack[depth].base  = 0;
            stack[depth].desc  = 0;
            stack[depth].aux   = 0;
            depth++;
            goto L_skip_object;
        case '[':
            idx++;
            stack[depth - 1].state = ST_SKIP_OBJECT;
            stack[depth].state = ST_SKIP_ARRAY;
            stack[depth].base  = 0;
            stack[depth].desc  = 0;
            stack[depth].aux   = 0;
            depth++;
            goto L_skip_array;
        case '"':
            idx++;
            if (scan_string(src, src_len, &idx, &str_start, &str_end, &has_escape)) {
                SUSPEND_EOF(skip_entry);
            }
            goto L_skip_object;
        case '-': case '0': case '1': case '2': case '3': case '4':
        case '5': case '6': case '7': case '8': case '9':
            num_start = idx;
            if (scan_number(src, src_len, &idx)) {
                SUSPEND_EOF(skip_entry);
            }
            goto L_skip_object;
        case 't':
            if (idx + 4 > src_len) SUSPEND_EOF(skip_entry);
            idx += 4;
            goto L_skip_object;
        case 'f':
            if (idx + 5 > src_len) SUSPEND_EOF(skip_entry);
            idx += 5;
            goto L_skip_object;
        case 'n':
            if (idx + 4 > src_len) SUSPEND_EOF(skip_entry);
            idx += 4;
            goto L_skip_object;
        default:
            ERROR_AT(idx, EXIT_SYNTAX_ERROR);
        }
    }

/* ==============================================================
 *  L_skip_array — Skip an entire JSON array (streaming)
 * ============================================================== */
L_skip_array:
    stack[depth - 1].state = ST_SKIP_ARRAY;
    SKIP_WS();
    if (idx >= src_len) {
        SUSPEND_EOF(idx);
    }
    if (src[idx] == ']') {
        idx++;
        STACK_POP();
        goto L_value_done;
    }
    if (src[idx] == ',') {
        idx++;
        SKIP_WS();
    }
    {
        uint32_t elem_start = idx;
        if (idx >= src_len) SUSPEND_EOF(elem_start);
        switch (src[idx]) {
        case '{':
            idx++;
            stack[depth - 1].state = ST_SKIP_ARRAY;
            stack[depth].state = ST_SKIP_OBJECT;
            stack[depth].base  = 0;
            stack[depth].desc  = 0;
            stack[depth].aux   = 0;
            depth++;
            goto L_skip_object;
        case '[':
            idx++;
            stack[depth - 1].state = ST_SKIP_ARRAY;
            stack[depth].state = ST_SKIP_ARRAY;
            stack[depth].base  = 0;
            stack[depth].desc  = 0;
            stack[depth].aux   = 0;
            depth++;
            goto L_skip_array;
        case '"':
            idx++;
            if (scan_string(src, src_len, &idx, &str_start, &str_end, &has_escape)) {
                SUSPEND_EOF(elem_start);
            }
            goto L_skip_array;
        case '-': case '0': case '1': case '2': case '3': case '4':
        case '5': case '6': case '7': case '8': case '9':
            num_start = idx;
            if (scan_number(src, src_len, &idx)) {
                SUSPEND_EOF(elem_start);
            }
            goto L_skip_array;
        case 't':
            if (idx + 4 > src_len) SUSPEND_EOF(elem_start);
            idx += 4;
            goto L_skip_array;
        case 'f':
            if (idx + 5 > src_len) SUSPEND_EOF(elem_start);
            idx += 5;
            goto L_skip_array;
        case 'n':
            if (idx + 4 > src_len) SUSPEND_EOF(elem_start);
            idx += 4;
            goto L_skip_array;
        default:
            ERROR_AT(idx, EXIT_SYNTAX_ERROR);
        }
    }

/* ==============================================================
 *  Stub labels for unimplemented states (Phase 2+)
 * ============================================================== */
/* ==============================================================
 *  L_slice_body — Parse slice elements: [ value, ... ]
 *  '[' already consumed. Go has allocated backing array.
 *  ctx->cur_base = backing array pointer (set by Go alloc handler).
 *  frame.aux = count (lo31) | value_done_flag (hi1).
 *  frame.aux2 = cap (0 → read from yield_param1).
 * ============================================================== */
L_slice_body: {
    stack[depth - 1].state = ST_SLICE_BODY;
    GsdecFrame *sf = &stack[depth - 1];
    const DecSliceDesc *sd = (const DecSliceDesc *)sf->desc;
    uint8_t *slice_target = sf->base;

    uint32_t raw_count = sf->aux;
    int value_done = (raw_count & 0x80000000u) != 0;
    uint32_t s_count = raw_count & 0x7FFFFFFFu;
    uint32_t s_cap = sf->aux2;
    if (s_cap == 0 && sf->aux2 != 0xFFFFFFFF) {
        /* First entry or after grow: read fresh cap from yield_param1 */
        s_cap = (uint32_t)ctx->yield_param1;
    }
    if (s_cap == 0xFFFFFFFF) {
        /* Sentinel: needs init (came from EOF before alloc).
         * We haven't allocated yet. Check empty/alloc now. */
        SKIP_WS();
        if (idx >= src_len) SUSPEND_EOF(idx);
        if (src[idx] == ']') {
            idx++;
            if (slice_target) { for (int i = 0; i < 24; i++) slice_target[i] = 0; }
            STACK_POP();
            goto L_value_done;
        }
        /* Yield for alloc */
        ctx->idx = idx;
        ctx->yield_param0 = (uint64_t)(uintptr_t)sd->codec_ptr;
        ctx->yield_param1 = (uint64_t)(uintptr_t)slice_target;
        ctx->yield_param2 = (uint64_t)(uintptr_t)sd->elem_rtype;
        sf->aux2 = 0; /* will read cap from yield_param1 on next entry */
        SUSPEND_YIELD(YIELD_ALLOC_SLICE);
    }

    uint8_t *s_base = ctx->cur_base;
    uint32_t elem_size = sd->elem_size;
    uint8_t  elem_kind = sd->elem_kind;
    const uint8_t *elem_desc = (const uint8_t *)sd->elem_desc;

    if (value_done) {
        s_count++;
    }

    for (;;) {
        SKIP_WS();
        if (idx >= src_len) {
            sf->aux = s_count;
            sf->aux2 = s_cap;
            SUSPEND_EOF(idx);
        }

        if (src[idx] == ']') {
            idx++;
            /* Write Len to slice header (offset 8, sizeof(uintptr_t)) */
            if (slice_target) {
                *(uintptr_t *)(slice_target + sizeof(uintptr_t)) = (uintptr_t)s_count;
            }
            STACK_POP();
            goto L_value_done;
        }

        if (src[idx] == ',') {
            idx++;
            SKIP_WS();
        }

        uint32_t elem_start = idx;

        if (idx >= src_len) {
            sf->aux = s_count;
            sf->aux2 = s_cap;
            SUSPEND_EOF(elem_start);
        }

        /* Check capacity */
        if (s_count >= s_cap) {
            ctx->idx = idx;
            ctx->yield_param0 = (uint64_t)s_count;
            ctx->yield_param1 = (uint64_t)(uintptr_t)slice_target;
            ctx->yield_param2 = (uint64_t)(uintptr_t)sd->elem_rtype;
            sf->aux = s_count;
            sf->aux2 = 0; /* cap=0: read fresh from yield_param1 on resume */
            SUSPEND_YIELD(YIELD_GROW_SLICE);
        }

        uint8_t *elem_ptr = s_base + (uintptr_t)s_count * elem_size;
        target = elem_ptr;
        val_kind = elem_kind;
        val_desc = elem_desc;

        /* Inline primitive elements */
        switch (src[idx]) {
        case '"':
            idx++;
            if (scan_string(src, src_len, &idx, &str_start, &str_end, &has_escape)) {
                sf->aux = s_count; sf->aux2 = s_cap;
                SUSPEND_EOF(elem_start);
            }
            if (elem_kind == KIND_STRING) {
                if (!has_escape) {
                    write_string(elem_ptr, src + str_start, str_end - str_start);
                } else {
                    uint32_t raw_len = str_end - str_start;
                    uint32_t arena_used = ctx->scratch_len;
                    uint32_t arena_cap = ctx->scratch_cap;
                    if (arena_used + raw_len > arena_cap) {
                        ctx->yield_param0 = (uint64_t)raw_len;
                        ctx->exit_code = YIELD_ARENA_FULL;
                        sf->aux = s_count; sf->aux2 = s_cap;
                        ctx->idx = elem_start;
                        ctx->resume_depth = depth;
                        return;
                    }
                    uint8_t *arena_dst = ctx->scratch_ptr + arena_used;
                    uint32_t written = unescape_to(src + str_start, raw_len, arena_dst, arena_cap - arena_used);
                    write_string(elem_ptr, arena_dst, written);
                    ctx->scratch_len = arena_used + written;
                }
            }
            s_count++;
            continue;
        case '-': case '0': case '1': case '2': case '3': case '4':
        case '5': case '6': case '7': case '8': case '9':
            num_start = idx;
            if (scan_number(src, src_len, &idx)) {
                sf->aux = s_count; sf->aux2 = s_cap;
                SUSPEND_EOF(elem_start);
            }
            if (kind_is_signed_int(elem_kind)) {
                write_int(elem_ptr, elem_kind, parse_int64(src, num_start, idx));
            } else if (kind_is_unsigned_int(elem_kind)) {
                write_uint(elem_ptr, elem_kind, parse_uint64(src, num_start, idx));
            } else if (elem_kind == KIND_FLOAT64) {
                *(double*)elem_ptr = parse_float64(src, num_start, idx);
            } else if (elem_kind == KIND_FLOAT32) {
                *(float*)elem_ptr = (float)parse_float64(src, num_start, idx);
            }
            s_count++;
            continue;
        case 't':
            if (idx + 4 > src_len) { sf->aux = s_count; sf->aux2 = s_cap; SUSPEND_EOF(elem_start); }
            idx += 4;
            if (elem_kind == KIND_BOOL) write_bool(elem_ptr, 1);
            s_count++;
            continue;
        case 'f':
            if (idx + 5 > src_len) { sf->aux = s_count; sf->aux2 = s_cap; SUSPEND_EOF(elem_start); }
            idx += 5;
            if (elem_kind == KIND_BOOL) write_bool(elem_ptr, 0);
            s_count++;
            continue;
        case 'n':
            if (idx + 4 > src_len) { sf->aux = s_count; sf->aux2 = s_cap; SUSPEND_EOF(elem_start); }
            idx += 4;
            s_count++;
            continue;
        case '{':
            idx++;
            sf->aux = s_count | 0x80000000u; /* value_done on resume */
            sf->aux2 = s_cap;
            sf->state = ST_SLICE_BODY; /* resume here after child */
            if (elem_kind == KIND_STRUCT) {
                stack[depth].state = ST_OBJECT_BODY;
                stack[depth].base  = elem_ptr;
                stack[depth].desc  = elem_desc;
                stack[depth].aux   = 0;
                depth++;
                goto L_object_body;
            }
            stack[depth].state = ST_SKIP_OBJECT;
            stack[depth].base  = 0;
            stack[depth].desc  = 0;
            stack[depth].aux   = 0;
            depth++;
            goto L_skip_object;
        case '[':
            idx++;
            sf->aux = s_count | 0x80000000u;
            sf->aux2 = s_cap;
            sf->state = ST_SLICE_BODY;
            /* Nested slice/array — skip for now */
            stack[depth].state = ST_SKIP_ARRAY;
            stack[depth].base  = 0;
            stack[depth].desc  = 0;
            stack[depth].aux   = 0;
            depth++;
            goto L_skip_array;
        default:
            ERROR_AT(idx, EXIT_SYNTAX_ERROR);
        }
    }
}

/* L_slice_next not used — slice body is self-contained with inner loop */
L_slice_next:
    goto L_slice_body;

/* ==============================================================
 *  L_array_body — Parse fixed-length array elements: [ value, ... ]
 *  '[' already consumed. No allocation needed (inline in parent struct).
 *  frame.aux = count, frame.aux2 = array_len (capacity).
 * ============================================================== */
L_array_body: {
    stack[depth - 1].state = ST_ARRAY_BODY;
    GsdecFrame *af = &stack[depth - 1];
    const DecArrayDesc *ad = (const DecArrayDesc *)af->desc;
    uint8_t *arr_target = af->base;

    uint32_t raw_count = af->aux;
    int a_value_done = (raw_count & 0x80000000u) != 0;
    uint32_t a_count = raw_count & 0x7FFFFFFFu;
    uint32_t a_cap = af->aux2;
    uint32_t a_elem_size = ad->elem_size;
    uint8_t  a_elem_kind = ad->elem_kind;
    const uint8_t *a_elem_desc = (const uint8_t *)ad->elem_desc;

    if (a_value_done) {
        a_count++;
    }

    for (;;) {
        SKIP_WS();
        if (idx >= src_len) {
            af->aux = a_count; af->aux2 = a_cap;
            SUSPEND_EOF(idx);
        }

        if (src[idx] == ']') {
            idx++;
            STACK_POP();
            goto L_value_done;
        }

        if (src[idx] == ',') {
            idx++;
            SKIP_WS();
        }

        uint32_t a_elem_start = idx;
        if (idx >= src_len) {
            af->aux = a_count; af->aux2 = a_cap;
            SUSPEND_EOF(a_elem_start);
        }

        /* Overflow: skip elements beyond array length */
        if (a_count >= a_cap) {
            switch (src[idx]) {
            case '"':
                idx++;
                if (scan_string(src, src_len, &idx, &str_start, &str_end, &has_escape)) {
                    af->aux = a_count; af->aux2 = a_cap;
                    SUSPEND_EOF(a_elem_start);
                }
                a_count++;
                continue;
            case '-': case '0': case '1': case '2': case '3': case '4':
            case '5': case '6': case '7': case '8': case '9':
                num_start = idx;
                if (scan_number(src, src_len, &idx)) {
                    af->aux = a_count; af->aux2 = a_cap;
                    SUSPEND_EOF(a_elem_start);
                }
                a_count++;
                continue;
            case 't':
                if (idx + 4 > src_len) { af->aux = a_count; af->aux2 = a_cap; SUSPEND_EOF(a_elem_start); }
                idx += 4; a_count++; continue;
            case 'f':
                if (idx + 5 > src_len) { af->aux = a_count; af->aux2 = a_cap; SUSPEND_EOF(a_elem_start); }
                idx += 5; a_count++; continue;
            case 'n':
                if (idx + 4 > src_len) { af->aux = a_count; af->aux2 = a_cap; SUSPEND_EOF(a_elem_start); }
                idx += 4; a_count++; continue;
            case '{':
                idx++;
                af->aux = a_count | 0x80000000u; af->aux2 = a_cap;
                af->state = ST_ARRAY_BODY;
                stack[depth].state = ST_SKIP_OBJECT;
                stack[depth].base = 0; stack[depth].desc = 0; stack[depth].aux = 0;
                depth++;
                goto L_skip_object;
            case '[':
                idx++;
                af->aux = a_count | 0x80000000u; af->aux2 = a_cap;
                af->state = ST_ARRAY_BODY;
                stack[depth].state = ST_SKIP_ARRAY;
                stack[depth].base = 0; stack[depth].desc = 0; stack[depth].aux = 0;
                depth++;
                goto L_skip_array;
            default:
                ERROR_AT(idx, EXIT_SYNTAX_ERROR);
            }
        }

        /* Parse element into array slot */
        uint8_t *a_elem_ptr = arr_target + (uintptr_t)a_count * a_elem_size;

        switch (src[idx]) {
        case '"':
            idx++;
            if (scan_string(src, src_len, &idx, &str_start, &str_end, &has_escape)) {
                af->aux = a_count; af->aux2 = a_cap;
                SUSPEND_EOF(a_elem_start);
            }
            if (a_elem_kind == KIND_STRING) {
                if (!has_escape) {
                    write_string(a_elem_ptr, src + str_start, str_end - str_start);
                } else {
                    uint32_t raw_len = str_end - str_start;
                    uint32_t arena_used = ctx->scratch_len;
                    uint32_t arena_cap = ctx->scratch_cap;
                    if (arena_used + raw_len > arena_cap) {
                        ctx->yield_param0 = (uint64_t)raw_len;
                        ctx->exit_code = YIELD_ARENA_FULL;
                        af->aux = a_count; af->aux2 = a_cap;
                        ctx->idx = a_elem_start;
                        ctx->resume_depth = depth;
                        return;
                    }
                    uint8_t *arena_dst = ctx->scratch_ptr + arena_used;
                    uint32_t written = unescape_to(src + str_start, raw_len, arena_dst, arena_cap - arena_used);
                    write_string(a_elem_ptr, arena_dst, written);
                    ctx->scratch_len = arena_used + written;
                }
            }
            a_count++;
            continue;
        case '-': case '0': case '1': case '2': case '3': case '4':
        case '5': case '6': case '7': case '8': case '9':
            num_start = idx;
            if (scan_number(src, src_len, &idx)) {
                af->aux = a_count; af->aux2 = a_cap;
                SUSPEND_EOF(a_elem_start);
            }
            if (kind_is_signed_int(a_elem_kind)) {
                write_int(a_elem_ptr, a_elem_kind, parse_int64(src, num_start, idx));
            } else if (kind_is_unsigned_int(a_elem_kind)) {
                write_uint(a_elem_ptr, a_elem_kind, parse_uint64(src, num_start, idx));
            } else if (a_elem_kind == KIND_FLOAT64) {
                *(double*)a_elem_ptr = parse_float64(src, num_start, idx);
            } else if (a_elem_kind == KIND_FLOAT32) {
                *(float*)a_elem_ptr = (float)parse_float64(src, num_start, idx);
            }
            a_count++;
            continue;
        case 't':
            if (idx + 4 > src_len) { af->aux = a_count; af->aux2 = a_cap; SUSPEND_EOF(a_elem_start); }
            idx += 4;
            if (a_elem_kind == KIND_BOOL) write_bool(a_elem_ptr, 1);
            a_count++;
            continue;
        case 'f':
            if (idx + 5 > src_len) { af->aux = a_count; af->aux2 = a_cap; SUSPEND_EOF(a_elem_start); }
            idx += 5;
            if (a_elem_kind == KIND_BOOL) write_bool(a_elem_ptr, 0);
            a_count++;
            continue;
        case 'n':
            if (idx + 4 > src_len) { af->aux = a_count; af->aux2 = a_cap; SUSPEND_EOF(a_elem_start); }
            idx += 4;
            a_count++;
            continue;
        case '{':
            idx++;
            af->aux = a_count | 0x80000000u; af->aux2 = a_cap;
            af->state = ST_ARRAY_BODY;
            if (a_elem_kind == KIND_STRUCT) {
                stack[depth].state = ST_OBJECT_BODY;
                stack[depth].base  = a_elem_ptr;
                stack[depth].desc  = a_elem_desc;
                stack[depth].aux   = 0;
                depth++;
                goto L_object_body;
            }
            stack[depth].state = ST_SKIP_OBJECT;
            stack[depth].base = 0; stack[depth].desc = 0; stack[depth].aux = 0;
            depth++;
            goto L_skip_object;
        case '[':
            idx++;
            af->aux = a_count | 0x80000000u; af->aux2 = a_cap;
            af->state = ST_ARRAY_BODY;
            stack[depth].state = ST_SKIP_ARRAY;
            stack[depth].base = 0; stack[depth].desc = 0; stack[depth].aux = 0;
            depth++;
            goto L_skip_array;
        default:
            ERROR_AT(idx, EXIT_SYNTAX_ERROR);
        }
    }
}

L_array_next:
    goto L_array_body;

/* ==============================================================
 *  L_map_body — Parse map entries: { "key": value, ... }
 *  '{' already consumed. Go has created the map and entry buffer.
 *  ctx->cur_base = entry buffer.
 *  Entry layout: [key_ptr:8][key_len:8][value:val_size]
 *  frame.aux = count (lo31) | value_done (hi1).
 *  frame.aux2 = buf_cap (0 → read from yield_param1).
 * ============================================================== */
L_map_body: {
    stack[depth - 1].state = ST_MAP_BODY;
    GsdecFrame *mf = &stack[depth - 1];
    const DecMapDesc *md = (const DecMapDesc *)mf->desc;
    uint8_t *map_target = mf->base;

    uint32_t raw_count = mf->aux;
    int m_value_done = (raw_count & 0x80000000u) != 0;
    uint32_t m_count = raw_count & 0x7FFFFFFFu;
    uint32_t m_buf_cap = mf->aux2;

    if (m_buf_cap == 0xFFFFFFFF) {
        /* Sentinel: needs init (came from EOF before map_init).
         * Check empty and yield MAP_INIT. */
        SKIP_WS();
        if (idx >= src_len) SUSPEND_EOF(idx);
        {
            int is_empty = (src[idx] == '}');
            if (is_empty) idx++;
            ctx->idx = idx;
            ctx->yield_param0 = (uint64_t)(uintptr_t)md->codec_ptr;
            ctx->yield_param1 = (uint64_t)(uintptr_t)map_target;
            ctx->yield_param2 = is_empty ? 1 : 0;
            mf->aux2 = 0;
            SUSPEND_YIELD(YIELD_MAP_INIT);
        }
    }

    if (m_buf_cap == 0 && mf->aux2 != 0xFFFFFFFF) {
        m_buf_cap = (uint32_t)ctx->yield_param1;
    }

    /* buf_cap == 0 means empty map (Go set it) → done */
    if (m_buf_cap == 0) {
        STACK_POP();
        goto L_value_done;
    }

    uint8_t *m_entry_buf = ctx->cur_base;
    uint32_t m_val_size = md->val_size;
    uint32_t m_stride = 16 + m_val_size;
    uint8_t  m_val_kind = md->val_kind;
    const uint8_t *m_val_desc = (const uint8_t *)md->val_desc;

    if (m_value_done) {
        m_count++;
    }

    for (;;) {
        SKIP_WS();
        if (idx >= src_len) {
            mf->aux = m_count; mf->aux2 = m_buf_cap;
            ctx->yield_param0 = (uint64_t)m_count;
            SUSPEND_EOF(idx);
        }

        if (src[idx] == '}') {
            idx++;
            ctx->idx = idx;
            if (m_count > 0) {
                ctx->yield_param0 = (uint64_t)m_count;
                ctx->yield_param2 = 1; /* done=true */
                STACK_POP(); /* pop map frame so resume goes to parent */
                SUSPEND_YIELD(YIELD_MAP_ASSIGN);
            }
            STACK_POP();
            goto L_value_done;
        }

        if (src[idx] == ',') {
            idx++;
            SKIP_WS();
        }

        uint32_t m_entry_start = idx;

        if (idx >= src_len) {
            mf->aux = m_count; mf->aux2 = m_buf_cap;
            ctx->yield_param0 = (uint64_t)m_count;
            SUSPEND_EOF(m_entry_start);
        }

        /* Buffer full: yield MAP_ASSIGN to flush entries */
        if (m_count >= m_buf_cap) {
            ctx->idx = idx;
            ctx->yield_param0 = (uint64_t)m_count;
            ctx->yield_param2 = 0; /* done=false */
            mf->aux = 0; mf->aux2 = m_buf_cap; /* reset count to 0 after flush */
            SUSPEND_YIELD(YIELD_MAP_ASSIGN);
        }

        /* Parse key */
        if (src[idx] != '"') ERROR_AT(idx, EXIT_SYNTAX_ERROR);
        idx++;
        uint32_t mk_start, mk_end;
        int mk_esc;
        if (scan_string(src, src_len, &idx, &mk_start, &mk_end, &mk_esc)) {
            mf->aux = m_count; mf->aux2 = m_buf_cap;
            SUSPEND_EOF(m_entry_start);
        }

        /* Write key to entry buffer */
        uint8_t *m_entry_ptr = m_entry_buf + (uintptr_t)m_count * m_stride;
        if (!mk_esc) {
            *(uintptr_t *)m_entry_ptr = (uintptr_t)(src + mk_start);
            *((uintptr_t *)m_entry_ptr + 1) = (uintptr_t)(mk_end - mk_start);
        } else {
            uint32_t raw_len = mk_end - mk_start;
            uint32_t arena_used = ctx->scratch_len;
            uint32_t arena_cap = ctx->scratch_cap;
            if (arena_used + raw_len > arena_cap) {
                ctx->yield_param0 = (uint64_t)raw_len;
                ctx->exit_code = YIELD_ARENA_FULL;
                mf->aux = m_count; mf->aux2 = m_buf_cap;
                ctx->idx = m_entry_start;
                ctx->resume_depth = depth;
                return;
            }
            uint8_t *arena_dst = ctx->scratch_ptr + arena_used;
            uint32_t written = unescape_to(src + mk_start, raw_len, arena_dst, arena_cap - arena_used);
            *(uintptr_t *)m_entry_ptr = (uintptr_t)arena_dst;
            *((uintptr_t *)m_entry_ptr + 1) = (uintptr_t)written;
            ctx->scratch_len = arena_used + written;
        }

        /* Colon */
        SKIP_WS();
        if (idx >= src_len) {
            mf->aux = m_count; mf->aux2 = m_buf_cap;
            SUSPEND_EOF(m_entry_start);
        }
        if (src[idx] != ':') ERROR_AT(idx, EXIT_SYNTAX_ERROR);
        idx++;
        SKIP_WS();

        /* Value: parse into entry buffer at offset 16 */
        uint8_t *m_val_ptr = m_entry_ptr + 16;

        if (idx >= src_len) {
            mf->aux = m_count; mf->aux2 = m_buf_cap;
            SUSPEND_EOF(m_entry_start);
        }

        /* Any/interface{}: use generic value dispatch via L_parse_value.
         * Set target/val_kind/val_desc so L_parse_value handles it.
         * On completion, L_value_done → DISPATCH_TOP → L_map_body (value_done). */
        if (m_val_kind == KIND_ANY) {
            target = m_val_ptr;
            val_kind = KIND_ANY;
            val_desc = 0;
            mf->aux = m_count | 0x80000000u; mf->aux2 = m_buf_cap;
            mf->state = ST_MAP_BODY;
            goto L_parse_value;
        }

        /* Inline primitives */
        switch (src[idx]) {
        case '"':
            idx++;
            if (scan_string(src, src_len, &idx, &str_start, &str_end, &has_escape)) {
                mf->aux = m_count; mf->aux2 = m_buf_cap;
                SUSPEND_EOF(m_entry_start);
            }
            if (m_val_kind == KIND_STRING) {
                if (!has_escape) {
                    write_string(m_val_ptr, src + str_start, str_end - str_start);
                } else {
                    uint32_t raw_len = str_end - str_start;
                    uint32_t arena_used = ctx->scratch_len;
                    uint32_t arena_cap = ctx->scratch_cap;
                    if (arena_used + raw_len > arena_cap) {
                        ctx->yield_param0 = (uint64_t)raw_len;
                        ctx->exit_code = YIELD_ARENA_FULL;
                        mf->aux = m_count; mf->aux2 = m_buf_cap;
                        ctx->idx = m_entry_start;
                        ctx->resume_depth = depth;
                        return;
                    }
                    uint8_t *arena_dst = ctx->scratch_ptr + arena_used;
                    uint32_t written = unescape_to(src + str_start, raw_len, arena_dst, arena_cap - arena_used);
                    write_string(m_val_ptr, arena_dst, written);
                    ctx->scratch_len = arena_used + written;
                }
            }
            m_count++;
            continue;
        case '-': case '0': case '1': case '2': case '3': case '4':
        case '5': case '6': case '7': case '8': case '9':
            num_start = idx;
            if (scan_number(src, src_len, &idx)) {
                mf->aux = m_count; mf->aux2 = m_buf_cap;
                SUSPEND_EOF(m_entry_start);
            }
            if (kind_is_signed_int(m_val_kind)) {
                write_int(m_val_ptr, m_val_kind, parse_int64(src, num_start, idx));
            } else if (kind_is_unsigned_int(m_val_kind)) {
                write_uint(m_val_ptr, m_val_kind, parse_uint64(src, num_start, idx));
            } else if (m_val_kind == KIND_FLOAT64) {
                *(double*)m_val_ptr = parse_float64(src, num_start, idx);
            } else if (m_val_kind == KIND_FLOAT32) {
                *(float*)m_val_ptr = (float)parse_float64(src, num_start, idx);
            }
            m_count++;
            continue;
        case 't':
            if (idx + 4 > src_len) { mf->aux = m_count; mf->aux2 = m_buf_cap; SUSPEND_EOF(m_entry_start); }
            idx += 4;
            if (m_val_kind == KIND_BOOL) write_bool(m_val_ptr, 1);
            m_count++;
            continue;
        case 'f':
            if (idx + 5 > src_len) { mf->aux = m_count; mf->aux2 = m_buf_cap; SUSPEND_EOF(m_entry_start); }
            idx += 5;
            if (m_val_kind == KIND_BOOL) write_bool(m_val_ptr, 0);
            m_count++;
            continue;
        case 'n':
            if (idx + 4 > src_len) { mf->aux = m_count; mf->aux2 = m_buf_cap; SUSPEND_EOF(m_entry_start); }
            idx += 4;
            m_count++;
            continue;
        case '{':
            idx++;
            mf->aux = m_count | 0x80000000u; mf->aux2 = m_buf_cap;
            mf->state = ST_MAP_BODY;
            if (m_val_kind == KIND_STRUCT) {
                stack[depth].state = ST_OBJECT_BODY;
                stack[depth].base  = m_val_ptr;
                stack[depth].desc  = m_val_desc;
                stack[depth].aux   = 0;
                depth++;
                goto L_object_body;
            }
            stack[depth].state = ST_SKIP_OBJECT;
            stack[depth].base = 0; stack[depth].desc = 0; stack[depth].aux = 0;
            depth++;
            goto L_skip_object;
        case '[':
            idx++;
            mf->aux = m_count | 0x80000000u; mf->aux2 = m_buf_cap;
            mf->state = ST_MAP_BODY;
            stack[depth].state = ST_SKIP_ARRAY;
            stack[depth].base = 0; stack[depth].desc = 0; stack[depth].aux = 0;
            depth++;
            goto L_skip_array;
        default:
            ERROR_AT(idx, EXIT_SYNTAX_ERROR);
        }
    }
}

L_map_next:
    goto L_map_body;

/* ==============================================================
 *  L_parse_any_value — Tape mode: emit instruction for one value.
 *  Used for any/interface{} fields. Containers push stack frames.
 * ============================================================== */
L_parse_any_value:
    SKIP_WS();
    if (idx >= src_len) {
        /* For insn mode EOF, we need to save state. Push a ROOT frame
         * with KIND_ANY so we re-enter insn mode on resume. */
        stack[depth].state = ST_ROOT;
        stack[depth].base = target;
        stack[depth].val_kind = KIND_ANY;
        stack[depth].desc = 0;
        depth++;
        SUSPEND_EOF(idx);
    }

    /* Emit SET_TARGET here (AFTER initial EOF check) so insn isn't polluted
     * if we suspend before parsing any value bytes.
     * Save insn position — rollback on any leaf EOF. */
    uint32_t any_insn_save = ctx->insn_len;
    if (target) {
        insn_emit_set_target(ctx, target);
    }

#define ANY_SUSPEND_EOF(rollback) do { \
    ctx->insn_len = any_insn_save; \
    stack[depth].state = ST_ROOT; \
    stack[depth].base = target; \
    stack[depth].val_kind = KIND_ANY; \
    stack[depth].desc = 0; \
    depth++; \
    SUSPEND_EOF(rollback); \
} while(0)

    switch (src[idx]) {
    case 'n':
        if (idx + 4 > src_len) ANY_SUSPEND_EOF(idx);
        idx += 4;
        insn_emit_u8(ctx, INSN_TAG_EMIT_NULL);
        goto L_value_done;

    case 't':
        if (idx + 4 > src_len) ANY_SUSPEND_EOF(idx);
        idx += 4;
        insn_emit_u8(ctx, INSN_TAG_EMIT_TRUE);
        goto L_value_done;

    case 'f':
        if (idx + 5 > src_len) ANY_SUSPEND_EOF(idx);
        idx += 5;
        insn_emit_u8(ctx, INSN_TAG_EMIT_FALSE);
        goto L_value_done;

    case '"':
        idx++;
        {
            uint32_t any_str_start = idx;
            if (scan_string(src, src_len, &idx, &str_start, &str_end, &has_escape)) {
                ANY_SUSPEND_EOF(any_str_start - 1);
            }
            insn_emit_string(ctx, src + str_start, str_end - str_start);
        }
        goto L_value_done;

    case '-': case '0': case '1': case '2': case '3': case '4':
    case '5': case '6': case '7': case '8': case '9':
        {
            uint32_t any_num_start = idx;
            int neg = 0;
            uint32_t ni = idx;
            if (src[ni] == '-') { neg = 1; ni++; }
            if (ni >= src_len) ANY_SUSPEND_EOF(any_num_start);
            int is_int = 1;
            while (ni < src_len && src[ni] >= '0' && src[ni] <= '9') ni++;
            if (ni < src_len && (src[ni] == '.' || src[ni] == 'e' || src[ni] == 'E')) {
                is_int = 0;
            }
            if (ni >= src_len) {
                ANY_SUSPEND_EOF(any_num_start);
            }
            if (is_int) {
                int64_t ival = parse_int64(src, any_num_start, ni);
                idx = ni;
                insn_emit_int(ctx, ival);
            } else {
                idx = any_num_start;
                if (scan_number(src, src_len, &idx)) {
                    ANY_SUSPEND_EOF(any_num_start);
                }
                insn_emit_number(ctx, src + any_num_start, idx - any_num_start);
            }
        }
        goto L_value_done;

    case '{':
        idx++;
        insn_emit_make_object(ctx);
        stack[depth].state = ST_ANY_OBJECT_BODY;
        stack[depth].base  = target; /* for SET_TARGET on resume root */
        stack[depth].desc  = 0;
        stack[depth].aux   = 0;
        depth++;
        goto L_any_object_body;

    case '[':
        idx++;
        insn_emit_make_array(ctx);
        stack[depth].state = ST_ANY_ARRAY_BODY;
        stack[depth].base  = target;
        stack[depth].desc  = 0;
        stack[depth].aux   = 0;
        depth++;
        goto L_any_array_body;

    default:
        ERROR_AT(idx, EXIT_SYNTAX_ERROR);
    }

/* ==============================================================
 *  L_any_object_body — Tape mode object: SET_KEY + value → CLOSE_OBJECT
 * ============================================================== */
L_any_object_body: {
    stack[depth - 1].state = ST_ANY_OBJECT_BODY;

    for (;;) {
        SKIP_WS();
        if (idx >= src_len) SUSPEND_EOF(idx);

        if (src[idx] == '}') {
            idx++;
            insn_emit_u8(ctx, INSN_TAG_CLOSE_OBJECT);
            if (insn_flush_if_needed(ctx)) {
                STACK_POP();
                /* Tape flushed — resume will go to parent (who will see
                 * the close already emitted). */
                ctx->idx = idx;
                ctx->resume_depth = depth;
                return;
            }
            STACK_POP();
            goto L_value_done;
        }

        if (src[idx] == ',') {
            idx++;
            SKIP_WS();
        }

        /* Check insn capacity before entry */
        if (insn_near_full(ctx)) {
            ctx->idx = idx;
            ctx->exit_code = YIELD_INSN_FLUSH;
            ctx->resume_depth = depth;
            return;
        }

        if (idx >= src_len) SUSPEND_EOF(idx);

        uint32_t ao_entry_start = idx;
        uint32_t ao_insn_save = ctx->insn_len;

        /* Parse key */
        if (src[idx] != '"') ERROR_AT(idx, EXIT_SYNTAX_ERROR);
        idx++;
        uint32_t ao_ks, ao_ke;
        int ao_kesc;
        if (scan_string(src, src_len, &idx, &ao_ks, &ao_ke, &ao_kesc)) {
            ctx->insn_len = ao_insn_save;
            SUSPEND_EOF(ao_entry_start);
        }

        /* Emit SET_KEY */
        insn_emit_set_key(ctx, src + ao_ks, ao_ke - ao_ks);

        /* Colon */
        SKIP_WS();
        if (idx >= src_len) {
            ctx->insn_len = ao_insn_save;
            SUSPEND_EOF(ao_entry_start);
        }
        if (src[idx] != ':') ERROR_AT(idx, EXIT_SYNTAX_ERROR);
        idx++;
        SKIP_WS();

        if (idx >= src_len) {
            ctx->insn_len = ao_insn_save;
            SUSPEND_EOF(ao_entry_start);
        }

        /* Parse value — dispatch inline for primitives */
        switch (src[idx]) {
        case 'n':
            if (idx + 4 > src_len) { ctx->insn_len = ao_insn_save; SUSPEND_EOF(ao_entry_start); }
            idx += 4;
            insn_emit_u8(ctx, INSN_TAG_EMIT_NULL);
            continue;
        case 't':
            if (idx + 4 > src_len) { ctx->insn_len = ao_insn_save; SUSPEND_EOF(ao_entry_start); }
            idx += 4;
            insn_emit_u8(ctx, INSN_TAG_EMIT_TRUE);
            continue;
        case 'f':
            if (idx + 5 > src_len) { ctx->insn_len = ao_insn_save; SUSPEND_EOF(ao_entry_start); }
            idx += 5;
            insn_emit_u8(ctx, INSN_TAG_EMIT_FALSE);
            continue;
        case '"':
            idx++;
            if (scan_string(src, src_len, &idx, &str_start, &str_end, &has_escape)) {
                ctx->insn_len = ao_insn_save;
                SUSPEND_EOF(ao_entry_start);
            }
            insn_emit_string(ctx, src + str_start, str_end - str_start);
            continue;
        case '-': case '0': case '1': case '2': case '3': case '4':
        case '5': case '6': case '7': case '8': case '9': {
            uint32_t ao_ns = idx;
            int ao_neg = 0;
            uint32_t ao_ni = idx;
            if (src[ao_ni] == '-') { ao_neg = 1; ao_ni++; }
            if (ao_ni >= src_len) { ctx->insn_len = ao_insn_save; SUSPEND_EOF(ao_entry_start); }
            int ao_is_int = 1;
            while (ao_ni < src_len && src[ao_ni] >= '0' && src[ao_ni] <= '9') ao_ni++;
            if (ao_ni < src_len && (src[ao_ni] == '.' || src[ao_ni] == 'e' || src[ao_ni] == 'E'))
                ao_is_int = 0;
            if (ao_ni >= src_len) { ctx->insn_len = ao_insn_save; SUSPEND_EOF(ao_entry_start); }
            if (ao_is_int) {
                int64_t ao_iv = parse_int64(src, ao_ns, ao_ni);
                idx = ao_ni;
                insn_emit_int(ctx, ao_iv);
            } else {
                idx = ao_ns;
                if (scan_number(src, src_len, &idx)) {
                    ctx->insn_len = ao_insn_save; SUSPEND_EOF(ao_entry_start);
                }
                insn_emit_number(ctx, src + ao_ns, idx - ao_ns);
            }
            continue;
        }
        case '{':
            idx++;
            insn_emit_make_object(ctx);
            stack[depth].state = ST_ANY_OBJECT_BODY;
            stack[depth].base  = 0;
            stack[depth].desc  = 0;
            stack[depth].aux   = 0;
            depth++;
            goto L_any_object_body;
        case '[':
            idx++;
            insn_emit_make_array(ctx);
            stack[depth].state = ST_ANY_ARRAY_BODY;
            stack[depth].base  = 0;
            stack[depth].desc  = 0;
            stack[depth].aux   = 0;
            depth++;
            goto L_any_array_body;
        default:
            ERROR_AT(idx, EXIT_SYNTAX_ERROR);
        }
    }
}

/* ==============================================================
 *  L_any_array_body — Tape mode array: value, ... → CLOSE_ARRAY
 * ============================================================== */
L_any_array_body: {
    stack[depth - 1].state = ST_ANY_ARRAY_BODY;

    for (;;) {
        SKIP_WS();
        if (idx >= src_len) SUSPEND_EOF(idx);

        if (src[idx] == ']') {
            idx++;
            insn_emit_u8(ctx, INSN_TAG_CLOSE_ARRAY);
            if (insn_flush_if_needed(ctx)) {
                STACK_POP();
                ctx->idx = idx;
                ctx->resume_depth = depth;
                return;
            }
            STACK_POP();
            goto L_value_done;
        }

        if (src[idx] == ',') {
            idx++;
            SKIP_WS();
        }

        if (insn_near_full(ctx)) {
            ctx->idx = idx;
            ctx->exit_code = YIELD_INSN_FLUSH;
            ctx->resume_depth = depth;
            return;
        }

        if (idx >= src_len) SUSPEND_EOF(idx);

        uint32_t aa_elem_start = idx;

        switch (src[idx]) {
        case 'n':
            if (idx + 4 > src_len) SUSPEND_EOF(aa_elem_start);
            idx += 4;
            insn_emit_u8(ctx, INSN_TAG_EMIT_NULL);
            continue;
        case 't':
            if (idx + 4 > src_len) SUSPEND_EOF(aa_elem_start);
            idx += 4;
            insn_emit_u8(ctx, INSN_TAG_EMIT_TRUE);
            continue;
        case 'f':
            if (idx + 5 > src_len) SUSPEND_EOF(aa_elem_start);
            idx += 5;
            insn_emit_u8(ctx, INSN_TAG_EMIT_FALSE);
            continue;
        case '"':
            idx++;
            if (scan_string(src, src_len, &idx, &str_start, &str_end, &has_escape)) {
                SUSPEND_EOF(aa_elem_start);
            }
            insn_emit_string(ctx, src + str_start, str_end - str_start);
            continue;
        case '-': case '0': case '1': case '2': case '3': case '4':
        case '5': case '6': case '7': case '8': case '9': {
            uint32_t aa_ns = idx;
            uint32_t aa_ni = idx;
            if (src[aa_ni] == '-') aa_ni++;
            if (aa_ni >= src_len) SUSPEND_EOF(aa_elem_start);
            int aa_is_int = 1;
            while (aa_ni < src_len && src[aa_ni] >= '0' && src[aa_ni] <= '9') aa_ni++;
            if (aa_ni < src_len && (src[aa_ni] == '.' || src[aa_ni] == 'e' || src[aa_ni] == 'E'))
                aa_is_int = 0;
            if (aa_ni >= src_len) SUSPEND_EOF(aa_elem_start);
            if (aa_is_int) {
                idx = aa_ni;
                insn_emit_int(ctx, parse_int64(src, aa_ns, aa_ni));
            } else {
                idx = aa_ns;
                if (scan_number(src, src_len, &idx)) SUSPEND_EOF(aa_elem_start);
                insn_emit_number(ctx, src + aa_ns, idx - aa_ns);
            }
            continue;
        }
        case '{':
            idx++;
            insn_emit_make_object(ctx);
            stack[depth].state = ST_ANY_OBJECT_BODY;
            stack[depth].base  = 0;
            stack[depth].desc  = 0;
            stack[depth].aux   = 0;
            depth++;
            goto L_any_object_body;
        case '[':
            idx++;
            insn_emit_make_array(ctx);
            stack[depth].state = ST_ANY_ARRAY_BODY;
            stack[depth].base  = 0;
            stack[depth].desc  = 0;
            stack[depth].aux   = 0;
            depth++;
            goto L_any_array_body;
        default:
            ERROR_AT(idx, EXIT_SYNTAX_ERROR);
        }
    }
}
} /* end vj_gdec_exec */

/* ============================================================
 *  Resume entry point — called after Go handles a yield
 * ============================================================ */

void vj_gdec_resume(DecExecCtx *ctx) {
    /* Resume is the same as exec — the dispatch table
     * jumps to the correct label based on stack top. */
    vj_gdec_exec(ctx);
}
