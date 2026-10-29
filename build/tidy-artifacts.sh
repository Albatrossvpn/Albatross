#!/usr/bin/env bash
# Deletes the artifacts that finished runs have left in the repository.
#
# The account's Actions storage is small (0.5 GB on the free plan) and a full
# build is some 400 MB. Once it is full, uploads stop with "Artifact storage
# quota has been hit" until the usage is recalculated, which takes hours. A
# run needs what it stores only for the later jobs of its own run, so this
# clears the rest; runs still going keep theirs.
#
# Needs GH_TOKEN (the actions permission, write) and GITHUB_REPOSITORY.
# Never fails: a run goes on without the cleanup.
set -uo pipefail

repo="${GITHUB_REPOSITORY:?}"

running=""
for status in queued in_progress waiting pending requested; do
  ids="$(gh api --paginate "repos/$repo/actions/runs?status=$status&per_page=100" --jq '.workflow_runs[].id')" || {
    echo "::warning::The runs could not be listed, so nothing is deleted."
    exit 0
  }
  running+="$ids"$'\n'
done

# The whole list first: deleting while paging would skip some.
artifacts="$(gh api --paginate "repos/$repo/actions/artifacts?per_page=100" \
  --jq '.artifacts[] | select(.expired | not) | [.id, .workflow_run.id, .size_in_bytes] | @tsv')" || {
  echo "::warning::The artifacts could not be listed, so nothing is deleted."
  exit 0
}

count=0 bytes=0
while IFS=$'\t' read -r id run size; do
  [ -n "$id" ] || continue
  grep -qxF "$run" <<<"$running" && continue
  if gh api -X DELETE "repos/$repo/actions/artifacts/$id" --silent; then
    count=$((count + 1))
    bytes=$((bytes + size))
  else
    echo "::warning::Artifact $id could not be deleted."
  fi
done <<<"$artifacts"
echo "deleted $count artifacts, $((bytes / 1048576)) MB"
