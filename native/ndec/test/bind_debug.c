/* bind_debug.c runs one named fixture through the C driver against JSON from
 * stdin (or a file argument) and dumps the yield trace, machine state, tape
 * arena, and bound results. Build with `make -C native/ndec bind-debug` and
 * step into the machine under lldb from this binary. */
#include <stdio.h>
#include <string.h>

#include "bind_driver.h"

#include "bind_scenarios.h"

int main(int argc, char **argv) {
  if (argc < 2 || argc > 3) {
    fprintf(stderr, "usage: %s <fixture> [file.json]  (JSON on stdin without a file)\n", argv[0]);
    fprintf(stderr, "fixtures:\n");
    for (int i = 0; i < (int)(sizeof(g_fixtures) / sizeof(g_fixtures[0])); i++)
      fprintf(stderr, "  %s\n", g_fixtures[i].name);
    return 2;
  }
  const HxDbgFixture *fx_sel = NULL;
  for (int i = 0; i < (int)(sizeof(g_fixtures) / sizeof(g_fixtures[0])); i++) {
    if (strcmp(g_fixtures[i].name, argv[1]) == 0) {
      fx_sel = &g_fixtures[i];
      break;
    }
  }
  if (!fx_sel) {
    fprintf(stderr, "unknown fixture: %s\n", argv[1]);
    return 2;
  }

  static char buf[1 << 20];
  size_t len = 0;
  if (argc == 3) {
    FILE *f = fopen(argv[2], "rb");
    if (!f) {
      perror(argv[2]);
      return 2;
    }
    len = fread(buf, 1, sizeof(buf), f);
    fclose(f);
  } else {
    len = fread(buf, 1, sizeof(buf), stdin);
  }

  static FxTree tree;
  static HxDriver drv;
  fx_sel->run(&drv, &tree, buf, len);
  return 0;
}
