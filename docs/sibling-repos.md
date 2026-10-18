# Making the sibling projects shippable through L80

`L80 vast`, `L80 pwdet` and `L80 dashboard` work today against a local
checkout through their override variables (`L80_VAST`, `L80_PWDET`,
`L80_DASHBOARD`). Before they work for anyone else, each project needs a public
repository, a tag, and the changes below. Then `make bump-tools` pins them and
the next L80 release ships them.

Each item was found while wiring the project into L80.

## BB-VAST-L80 → github.com/launch80/BB-VAST-L80 (tag v0.1.0)

1. **BetterBench version check.** `doctor` (`src/bbvastl80/cli.py`, the
   "pin must be 0.4.0" probe) warns on anything but 0.4.0. L80 pins 0.6.1.
   Accept `>= 0.5.0`, or check `results.json` `schema == 2` instead.
2. **`L80_BIN` default.** `config.py` defaults to lowercase `l80`; the binary is
   `L80`. Try `L80`, then `l80`. (Under `L80 vast` it is set for you.)
3. **The Vast key and the working directory.** `Config.load` reads `./.env` from
   the current directory. Run through L80 from anywhere else, the key in the
   project's `.env` is not found. Document `~/.config/bbvastl80/credentials` as
   the place for the key (`init` could write there).
4. **Engines.** `sglang`, `tgi` and `ollama` are accepted by the spec but raise
   `KeyError` at provision time (`providers/engines.py`). Reject them in the spec
   validator with a clear message until they exist.
5. **`--json`** on `run`, `status` and `list` (one final object), so agents and
   L80 need not read state files. L80 reads `runs/<id>/state.json` meanwhile.
6. **No watchdog when killed.** If the process dies, the box bills until
   `destroy`/`reap`. L80 adds `--max-hours`; say so in the README.
7. `bench.py`'s docstring describes BetterBench 0.4.0 flags; 0.6.1 is compatible
   with every flag it passes.
8. Add the remote, push, `git tag v0.1.0`, push the tag.

## poisoned-weights-detector → github.com/launch80/poisoned-weights-detector (tag v0.1.0)

1. **Thresholds are read from the working directory.** `thresholds/default.yaml`
   is opened relative to the CWD (`cli.py`, `checks/blackbox/probes.py`,
   `fixture/evaluate.py`); when absent, built-in defaults are used without a
   warning. Installed through uvx there is no such file, so the tuned thresholds are
   lost silently. Ship it as package data (`src/pwdet/thresholds/default.yaml`,
   hatch include) and load it with `importlib.resources`; keep the CWD file only as
   an explicit override, and warn when falling back.
2. **Docker scripts are found by `Path(__file__).parents[3]`**
   (`whitebox/runner_docker.py`, `fixture/serve.py`, `cli.py`), which does not
   exist in a wheel, so white-box and fixture commands fail when installed. Ship
   `docker/` as package data, or fail with "white-box needs a source checkout".
3. **Privacy.** `report.json` records `host.hostname`, `host.platform` and absolute
   `subject.path`s. L80 strips them before publishing; a `scan --redact` option
   would let the project own that.
4. **Docs drift.** `docs/GUIDE.md` shows `probe run --models a,b` and
   `probe compare --suspect/--reference`, which do not exist.
5. **Exit codes.** `scan` exits 1 for "suspicious" and for an uncaught error. A
   distinct code for "pwdet failed" (e.g. 3) would make the verdict unambiguous.
6. Add the remote, push, tag.

## ai-command-dashboard → github.com/launch80/ai-command-dashboard (tag v0.1.0)

1. `git init`; `.gitignore` with `state/` and `__pycache__/`.
2. **Package it.** L80 runs a console script named `ai-dashboard` with
   subcommands `tui` and `serve` and a `--version`:
   - move the modules into `src/ai_command_dashboard/`, with `static/` and
     `helper/` as package data;
   - `pyproject.toml`: hatchling, `requires-python >= 3.10`, no dependencies,
     `[project.scripts] ai-dashboard = "ai_command_dashboard.cli:main"`;
   - `ai-dashboard tui [...]` takes `tui.py`'s flags; `ai-dashboard serve [...]`
     takes `server.py`'s.
3. **Paths.** Load `static/` with `importlib.resources`, not relative to the CWD.
   Default `AI_DASHBOARD_STATE` to `$XDG_STATE_HOME/ai-command-dashboard`; L80 sets
   it to `~/.local/state/L80/dashboard` when unset.
4. **Hardware assumptions** (follow-up, not blocking): collectors are Intel
   `xe`/`i915`-specific, the vLLM endpoint is fixed at `127.0.0.1:8000`, and the
   transcript reader expects the `pi` harness. Make the endpoint configurable
   first; NVIDIA (`nvidia-smi`) and AMD (`amdgpu` sysfs) collectors next.
5. Push, tag.

## Then, here

```sh
make bump-tools          # pins each tool to its checked-out, pushed HEAD (tools.local.json)
make check-manifest SMOKE=1
L80_VAST= L80_PWDET= L80_DASHBOARD= go run ./cmd/L80 tools check   # resolves through uvx now
```
