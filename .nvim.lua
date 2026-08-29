-- Project-local gopls hint whitelist.
--
-- gopls has no per-project config file and no //nolint-style suppression
-- (golang/go#63829). Some tests in this repo intentionally pin behavior
-- that gopls flags (e.g. omitempty on a struct-typed Value field), so we
-- drop those specific diagnostics at the LSP publishDiagnostics boundary
-- instead of disabling the analyzer project-wide.
--
-- Installed as a per-client handler on gopls only (vim.lsp.config), so
-- other LSP clients keep the default publishDiagnostics path untouched.
--
-- Add an entry per file with the message substrings to drop. Matching is
-- plain (find with plain=true), so substrings must match literally.

local suppress = {
  ["tests/value_test.go"] = {
    "Omitempty has no effect on nested struct fields",
  },
}

vim.lsp.config("gopls", {
  handlers = {
    ["textDocument/publishDiagnostics"] = function(err, result, ctx)
      if result and result.diagnostics then
        local bufnr = vim.uri_to_bufnr(result.uri)
        local rel = vim.fn.fnamemodify(vim.api.nvim_buf_get_name(bufnr), ":.")
        local pats = suppress[rel]
        if pats then
          local keep = {}
          for _, d in ipairs(result.diagnostics) do
            local drop = false
            for _, p in ipairs(pats) do
              if d.message:find(p, 1, true) then drop = true; break end
            end
            if not drop then keep[#keep + 1] = d end
          end
          result.diagnostics = keep
        end
      end
      vim.lsp.diagnostic.on_publish_diagnostics(err, result, ctx)
    end,
  },
})
