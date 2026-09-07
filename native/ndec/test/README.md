# bind 测试调试说明

## 构建与运行

```bash
make -C native/ndec test
make -C native/ndec bind-debug
make -C native/ndec bind-debug BIND_ASAN=1

./build/bind_debug dual_view xxx.json   # 场景名 + JSON 文件（不传文件则读 stdin）
```

场景：`struct`、`value`、`value_ta`、`vfield`、`dual_view`。

## 命令行 lldb

```bash
lldb native/ndec/build/bind_debug
(lldb) breakpoint set -f bind.h -l 791     # 断在 phase2_walk
(lldb) run dual_view /tmp/debug-ndec-input.json
(lldb) frame variable depth
(lldb) gui
```

## 调试提示

- `-DVJ_DEBUG` 已开：`vj_fprintf_stderr` 日志打到 stderr；`VJ_DEBUG_TAPE_BIND_GUARD` 在「输入 yield 出现在 tape bind 内」等不变量破坏时直接 `__builtin_trap`
- ASan 变体配合环境变量 `ASAN_OPTIONS=detect_leaks=0`（Run config 里加）。
- 机器对 arena 无边界检查，越界只靠 ASan 或 trap 守卫暴露。
