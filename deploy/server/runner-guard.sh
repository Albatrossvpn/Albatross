#!/usr/bin/env bash
# Locks the project's self-hosted runner (the one labelled "relay") to the
# maintenance workflow, .github/workflows/server.yml run by hand from main.
# A runner attached to a public repository takes jobs from any workflow
# whose runs-on matches, a pull request's from a fork included; the job
# started hook written here fails every other job before any of its steps
# runs. Run it as a job of server.yml itself (it finds the runner from the
# job's RUNNER_TEMP) or on the server with RUNNER_ROOT set; running it again
# changes nothing.
set -euo pipefail
root="${RUNNER_ROOT:-$(cd "${RUNNER_TEMP:?run this inside a runner job, or set RUNNER_ROOT}/../.." && pwd)}"
[ -f "$root/.runner" ] || { echo "no runner configured in $root" >&2; exit 1; }
hook=/usr/local/sbin/albatross-runner-hook.sh
mkdir -p "$(dirname "$hook")"
cat >"$hook.new" <<'EOF'
#!/bin/bash
# Albatross: this runner takes only the hand-run maintenance workflow
# (.github/workflows/server.yml on main, run by hand). Any other job, a
# pull request's above all, fails here before a single step of it runs.
# Written by deploy/server/runner-guard.sh.
want="Albatrossvpn/Albatross workflow_dispatch Albatrossvpn/Albatross/.github/workflows/server.yml@refs/heads/main"
got="${GITHUB_REPOSITORY:-} ${GITHUB_EVENT_NAME:-} ${GITHUB_WORKFLOW_REF:-}"
if [ "$got" = "$want" ]; then
  exit 0
fi
echo "This runner only runs the server workflow, by hand, from main; refused: $got" >&2
exit 1
EOF
chmod 755 "$hook.new"
# Inside a job: the job running this must pass the hook, or the next run of
# this workflow would be refused too and the server could only be reached
# by SSH. Checked before the hook is turned on.
if [ -n "${GITHUB_WORKFLOW_REF:-}" ] && ! bash -e "$hook.new"; then
  rm -f "$hook.new"
  echo "this job would not pass the hook; nothing changed" >&2
  exit 1
fi
mv "$hook.new" "$hook"
# The runner reads its environment from .env when it starts.
touch "$root/.env"
sed -i '/^ACTIONS_RUNNER_HOOK_JOB_STARTED=/d' "$root/.env"
echo "ACTIONS_RUNNER_HOOK_JOB_STARTED=$hook" >>"$root/.env"
svc="$(systemctl list-units --type=service --all --no-legend 'actions.runner.*' 2>/dev/null | awk '{print $1}' | head -1 || true)"
echo "runner in $root; hook $hook; service ${svc:-not found}"
if [ -n "$svc" ]; then
  # Restarted once this job is over, so it reads the hook.
  systemctl stop albatross-runner-restart.timer albatross-runner-restart.service >/dev/null 2>&1 || true
  systemd-run --quiet --on-active=20 --unit=albatross-runner-restart systemctl restart "$svc"
  echo "the runner restarts in 20 seconds and checks every job from then on"
else
  echo "restart the runner yourself so it reads $root/.env" >&2
fi
