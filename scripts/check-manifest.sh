#!/bin/sh
# Validate every pin in internal/pytool/manifest.json before a release:
#   - the commit can be fetched from the public repo;
#   - a tag ref really points at that commit;
#   - with --smoke, `uvx --from <pinned requirement> <exe> <version_cmd>` runs.
# Unpinned tools (empty commit) are reported and skipped.
# Requires: git, python3; uvx for --smoke.
set -eu
smoke=0
[ "${1:-}" = "--smoke" ] && smoke=1
manifest="internal/pytool/manifest.json"
[ -f "$manifest" ] || { echo "run from the repo root" >&2; exit 2; }
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
fail=0

python3 -I - "$manifest" > "$work/tools.tsv" <<'PY'
import json, sys
m = json.load(open(sys.argv[1]))
for name, t in sorted(m["tools"].items()):
    extras = ",".join(t.get("extras") or [])
    print("|".join([name, t["repo"], t.get("ref", ""), t.get("commit", ""), t["package"], t["exe"],
                     t.get("python", ""), extras, " ".join(t.get("version_cmd") or []) or "--help"]))
PY

while IFS="|" read -r name repo ref commit pkg exe python extras vcmd; do
  if [ -z "$commit" ]; then
    echo "$name: not pinned (skipped)"
    continue
  fi
  dir="$work/$name"
  git init -q "$dir"
  if ! git -C "$dir" fetch -q --depth=1 "$repo" "$commit" 2>"$work/err"; then
    echo "$name: FAIL commit $commit cannot be fetched from $repo: $(cat "$work/err")"
    fail=1
    continue
  fi
  if [ -n "$ref" ]; then
    tagged=$(git ls-remote "$repo" "refs/tags/$ref^{}" "refs/tags/$ref" | awk 'NR==1{print $1}')
    if [ -n "$tagged" ] && ! git ls-remote "$repo" "refs/tags/$ref^{}" "refs/tags/$ref" | grep -q "^$commit"; then
      echo "$name: FAIL tag $ref on $repo does not point at $commit"
      fail=1
      continue
    fi
  fi
  echo "$name: ok $ref ($commit)"
  if [ "$smoke" = 1 ]; then
    req="$pkg"
    [ -n "$extras" ] && req="$pkg[$extras]"
    req="$req @ git+$repo@$commit"
    set -- --from "$req" "$exe"
    [ -n "$python" ] && set -- --python "$python" "$@"
    # shellcheck disable=SC2086
    if ! (cd "$work" && uvx "$@" $vcmd >"$work/out" 2>&1); then
      echo "$name: FAIL smoke run of $req:"; tail -20 "$work/out"
      fail=1
    else
      echo "$name: smoke $(tail -1 "$work/out")"
    fi
  fi
done < "$work/tools.tsv"
exit $fail
