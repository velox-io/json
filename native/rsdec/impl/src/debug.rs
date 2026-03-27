//! Stderr debug output via raw syscall (no libc, no std).

const STDERR: usize = 2;

#[inline(never)]
fn write_stderr(buf: &[u8]) {
    unsafe {
        #[cfg(target_os = "macos")]
        {
            core::arch::asm!(
                "svc #0x80",
                in("x16") 0x2000004_usize,
                in("x0") STDERR,
                in("x1") buf.as_ptr(),
                in("x2") buf.len(),
                lateout("x0") _,
                options(nostack),
            );
        }
        #[cfg(target_os = "linux")]
        {
            core::arch::asm!(
                "svc #0",
                in("x8") 64_usize,
                in("x0") STDERR,
                in("x1") buf.as_ptr(),
                in("x2") buf.len(),
                lateout("x0") _,
                options(nostack),
            );
        }
    }
}

pub fn eprint(s: &str) {
    write_stderr(s.as_bytes());
}

pub fn eprintln(s: &str) {
    write_stderr(s.as_bytes());
    write_stderr(b"\n");
}

pub fn eprint_usize(label: &str, v: usize) {
    write_stderr(label.as_bytes());
    let mut buf = [b'0'; 20];
    let s = fmt_usize(v, &mut buf);
    write_stderr(s);
    write_stderr(b"\n");
}

pub fn eprint_u64(label: &str, v: u64) {
    eprint_usize(label, v as usize);
}

pub fn eprint_i32(label: &str, v: i32) {
    write_stderr(label.as_bytes());
    if v < 0 {
        write_stderr(b"-");
        let mut buf = [b'0'; 20];
        let s = fmt_usize((-(v as i64)) as usize, &mut buf);
        write_stderr(s);
    } else {
        let mut buf = [b'0'; 20];
        let s = fmt_usize(v as usize, &mut buf);
        write_stderr(s);
    }
    write_stderr(b"\n");
}

fn fmt_usize(mut v: usize, buf: &mut [u8; 20]) -> &[u8] {
    if v == 0 {
        return b"0";
    }
    let mut i = 20;
    while v > 0 {
        i -= 1;
        buf[i] = b'0' + (v % 10) as u8;
        v /= 10;
    }
    &buf[i..]
}

pub fn dump_ctx(tag: &str, ctx: &crate::DecExecCtx) {
    write_stderr(b"[RS ");
    write_stderr(tag.as_bytes());
    write_stderr(b"] idx=");
    let mut b1 = [b'0'; 20];
    write_stderr(fmt_usize(ctx.idx as usize, &mut b1));
    write_stderr(b" exit=");
    let mut b2 = [b'0'; 20];
    write_stderr(fmt_usize(ctx.exit_code as usize, &mut b2));
    write_stderr(b" depth=");
    let mut b3 = [b'0'; 20];
    write_stderr(fmt_usize(ctx.resume_depth as usize, &mut b3));
    write_stderr(b" p0=");
    let mut b4 = [b'0'; 20];
    write_stderr(fmt_usize(ctx.yield_param0 as usize, &mut b4));
    write_stderr(b" p1=");
    let mut b5 = [b'0'; 20];
    write_stderr(fmt_usize(ctx.yield_param1 as usize, &mut b5));
    write_stderr(b" srclen=");
    let mut b6 = [b'0'; 20];
    write_stderr(fmt_usize(ctx.src_len as usize, &mut b6));
    write_stderr(b"\n");
}
