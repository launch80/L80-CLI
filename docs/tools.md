# Pinned tools: how they are run, bumped and released

L80 fronts Python tools that live in their own repositories. None needs a
token. Each release of L80 pins every tool to one git commit, so a given L80
version always runs the same tool code, and `L80 update` is how users receive
a newer tool.

| Tool | Repository | Console script | Override |
|---|---|---|---|
| `betterbench` | https://github.com/GGZ14/BetterBench | `betterbench` | `L80_BETTERBENCH` |
| `vast` | https://github.com/launch80/BB-VAST-L80 | `bbvastl80` | `L80_VAST` |
| `pwdet` | https://github.com/launch80/poisoned-weights-detector | `pwdet` | `L80_PWDET` |
| `dashboard` | https://github.com/launch80/ai-command-dashboard | `ai-dashboard` | `L80_DASHBOARD` |

## The manifest

`internal/pytool/manifest.json` is embedded in the binary.

| Field | Meaning |
|---|---|
| `repo` | Public HTTPS git URL. |
| `ref` | Tag (or branch) the commit was taken from. Informational; CI checks a tag still points at the commit. |
| `commit` | Full 40-hex SHA. The only thing used at run time. Empty means "not pinned in this release". |
| `package` / `exe` | Python distribution name and the console script to run. |
| `env` | Override variable. Its value is a command line, used unchecked. |
| `python` | uv `--python` constraint, e.g. `3.12` for pwdet. |
| `extras` | Default extras. `<ENV>_EXTRAS` (e.g. `L80_PWDET_EXTRAS=gguf`) overrides; empty means none. |
| `min_version` | Oldest version accepted from PATH. |
| `version_cmd` | Arguments that print the version. Empty disables the PATH version check. |
| `notes` | One line for `L80 tools list`. |

## Resolution order

1. The override variable, if set.
2. The console script on PATH, if `<exe> <version_cmd>` reports at least `min_version`.
3. `uvx [--python X] --from "<package>[extras] @ git+<repo>@<commit>" <exe>`, when uvx is
   installed and the tool is pinned.
4. A PATH copy that is too old, with a warning, rather than nothing.

Otherwise the command fails with `E_TOOL_UNAVAILABLE` (exit 11) and a remedy.

## Releasing a change made in a tool's repository

1. In the tool's repository: commit, tag (e.g. `v0.1.1`), and push both. The
   commit must be on the public remote: a pin users cannot fetch breaks the
   command for everyone on that L80 version.
2. Here, with the local checkout paths in `tools.local.json` (gitignored):

   ```json
   { "vast": "/home/you/apps/BB-VAST-L80", "pwdet": "/home/you/apps/poisoned-weights-detector",
     "dashboard": "/home/you/apps/ai-command-dashboard" }
   ```

   run `make bump-tools` (or `make bump-tools ONLY=pwdet`). It pins each tool to the
   checked-out `HEAD`, refuses a dirty tree, and refuses a commit the remote does not
   have. `L80_TOOLS_LOCAL_<NAME>` overrides a path.
3. `make check-manifest SMOKE=1` fetches each pin and runs it through uvx.
4. Bump `VERSION` in the Makefile and `Version` in `internal/cli/version.go`, add a
   CHANGELOG entry, commit, and push a `v*` tag. The release workflow runs the tests and
   the manifest check before it builds anything.
5. Users run `L80 update`. The next invocation of the tool bootstraps the new pin.

BetterBench is upstream, not ours: bump its pin by hand (tag and commit from
https://github.com/GGZ14/BetterBench/tags), read its CHANGELOG for results.json
changes, add a fixture under `internal/betterbench/testdata/`, and confirm the
launch80 renderer supports the new version before releasing.
