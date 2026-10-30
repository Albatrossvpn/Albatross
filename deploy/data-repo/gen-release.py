#!/usr/bin/env python3
"""Writes the release workflow of the public repository alba-data from
.github/workflows/build.yml, so builds and releases run on that
repository's free minutes while this one stays private.

    python3 deploy/data-repo/gen-release.py > ../alba-data/.github/workflows/release.yml

Run it again whenever build.yml changes (CLAUDE.md). What it changes:

- It runs only by hand (workflow_dispatch), with the ref of this
  repository to build: no push or pull request starts it, so no fork's
  workflow ever sees its secrets or its cache.
- Every checkout is of this repository, with the SOURCE_TOKEN secret.
- Every build handed from one job to the next goes into the cache sealed
  (gpg, AES-256, with a key derived from TOKEN_SECRET) and is opened by
  the job that needs it; nothing readable is left in the public cache.
- The release, its files and the update manifest go to this repository
  (RELEASE_REPO), with SOURCE_TOKEN.
- Comments are left out: they describe this project's workings.

The cleanup workflow there deletes each run, its logs and its caches once
it is over (deploy/data-repo/README.md).
"""
import re
import sys
from pathlib import Path

PRIVATE = "Albatrossvpn/Albatross"
root = Path(__file__).resolve().parents[2]
src = (root / ".github/workflows/build.yml").read_text().splitlines()

HEADER = """name: Release

on:
  workflow_dispatch:
    inputs:
      ref:
        description: "Branch, tag or commit of the app to build"
        required: true
        default: main
      release:
        description: "Publish a release with this version (e.g. 1.2.0); empty only builds and tests"
        required: false
      force_update:
        description: "Older versions stop connecting until updated"
        type: boolean
        default: true
      update_message:
        description: "Shown with the update in installed copies"
        required: false

permissions:
  contents: read
  actions: write
"""

# A step that seals or opens a build for the cache. ${KEY_EXPR} is the
# passphrase: SHA-256 of TOKEN_SECRET with a label, never printed.
SEAL = """      - name: Seal for the cache
{cond}        shell: bash
        env:
          TOKEN_SECRET: ${{{{ secrets.TOKEN_SECRET }}}}
        run: |
          mkdir -p sealed
          key="$(printf 'alba-cache:%s' "$TOKEN_SECRET" | sha256sum | cut -d' ' -f1)"
          tar -cf - {paths} | gpg --batch --yes --quiet --pinentry-mode loopback --passphrase-fd 3 3<<<"$key" \\
            --symmetric --cipher-algo AES256 --s2k-digest-algo SHA512 --s2k-count 65011712 -o sealed/{name}.tar.gpg
"""
OPEN = """      - name: Open from the cache
{cond}        shell: bash
        env:
          TOKEN_SECRET: ${{{{ secrets.TOKEN_SECRET }}}}
        run: |
          key="$(printf 'alba-cache:%s' "$TOKEN_SECRET" | sha256sum | cut -d' ' -f1)"
          gpg --batch --quiet --pinentry-mode loopback --passphrase-fd 3 3<<<"$key" -d sealed/{name}.tar.gpg | tar -xf -
          rm -f sealed/{name}.tar.gpg
"""


def checkout(indent, extra):
    pad = " " * indent
    lines = [f"{pad}- uses: actions/checkout@v7", f"{pad}  with:", f"{pad}    repository: {PRIVATE}",
             f"{pad}    token: ${{{{ secrets.SOURCE_TOKEN || secrets.MYALBA }}}}"]
    if "ref" not in extra:
        lines.append(f"{pad}    ref: ${{{{ inputs.ref }}}}")
    lines += [f"{pad}    {k}: {v}" for k, v in extra.items()]
    # The domestic mirrors' addresses are masked in everything the job
    # prints after this, each one on its own (a secret is masked only whole).
    lines += [f"{pad}- name: Mask", f"{pad}  shell: bash", f"{pad}  env:",
              f"{pad}    DOMESTIC_MIRROR: ${{{{ secrets.DOMESTIC_MIRROR }}}}",
              f"{pad}    T: ${{{{ secrets.CLOUDFLARE_API_TOKEN }}}}",
              f"{pad}    A: ${{{{ secrets.CLOUDFLARE_ACCOUNT_ID }}}}",
              f"{pad}  run: |",
              f"{pad}    IFS=', ' read -ra m <<<\"${{DOMESTIC_MIRROR:-}}\"",
              f"{pad}    for x in \"${{m[@]}}\"; do [ -n \"$x\" ] && echo \"::add-mask::$x\" && echo \"::add-mask::${{x#*://}}\"; done",
              # The Cloudflare account, from the token when its secret is not set.
              f"{pad}    id=\"$A\"",
              f"{pad}    [ -n \"$id\" ] || [ -z \"$T\" ] || id=\"$(curl -fsS -H \"Authorization: Bearer $T\" https://api.cloudflare.com/client/v4/accounts 2>/dev/null | jq -r '.result[0].id // empty' 2>/dev/null)\"",
              f"{pad}    if [ -n \"$id\" ]; then echo \"::add-mask::$id\"; echo \"CF_ACCOUNT=$id\" >> \"$GITHUB_ENV\"; fi; true"]
    return lines


def block(lines, i):
    """The step starting at lines[i] (a "- uses:" or "- name:" line) and the
    index after it."""
    indent = len(lines[i]) - len(lines[i].lstrip())
    j = i + 1
    while j < len(lines) and (not lines[j].strip() or len(lines[j]) - len(lines[j].lstrip()) > indent):
        j += 1
    return lines[i:j], j


def field(step, name):
    for l in step:
        m = re.match(r"\s+(?:- )?" + name + r":\s*(.*)$", l)
        if m:
            return m.group(1).strip()
    return None


# Comments and the original trigger go; the rest is kept line for line.
body = [l for l in src if not re.match(r"^\s*#", l)]
start = next(i for i, l in enumerate(body) if l.startswith("concurrency:"))
body = body[start:]

out = HEADER.splitlines() + [""]
i = 0
while i < len(body):
    line = body[i]
    s = line.strip()
    if s.startswith("- uses: actions/checkout@"):
        step, i = block(body, i)
        extra = {}
        for l in step[1:]:
            m = re.match(r"\s+(ref|path):\s*(.*)$", l)
            if m:
                extra[m.group(1)] = m.group(2)
        out += checkout(len(line) - len(line.lstrip()), extra)
        continue
    if s.startswith("- uses: actions/cache/save@") or s.startswith("- uses: actions/cache/restore@"):
        step, i = block(body, i)
        key = field(step, "key") or ""
        name = key.split("-$", 1)[0]
        paths = field(step, "path")
        cond = field(step, "if")
        cond_line = f"        if: {cond}\n" if cond else ""
        new = []
        for l in step:
            if re.match(r"\s+path:", l):
                l = re.sub(r"path:.*", f"path: sealed/{name}.tar.gpg", l)
            new.append(l)
        if "save@" in s:
            out += SEAL.format(cond=cond_line, paths=paths, name=name).splitlines()
            out += new
        else:
            out += new
            out += OPEN.format(cond=cond_line, name=name).splitlines()
        continue
    out.append(line)
    i += 1

text = "\n".join(out) + "\n"
# The release is this repository's: its commit, its releases, its manifest.
text = text.replace("target_commitish: ${{ github.sha }}", "target_commitish: ${{ needs.build.outputs.sha }}\n          repository: " + PRIVATE)
text = text.replace("${{ secrets.RELEASE_TOKEN || github.token }}", "${{ secrets.SOURCE_TOKEN || secrets.MYALBA }}")
text = text.replace("          GH_TOKEN: ${{ secrets.SOURCE_TOKEN || secrets.MYALBA }}\n", "          GH_TOKEN: ${{ secrets.SOURCE_TOKEN || secrets.MYALBA }}\n          RELEASE_REPO: " + PRIVATE + "\n")
text = text.replace("      version: ${{ steps.version.outputs.version }}\n",
                    "      version: ${{ steps.version.outputs.version }}\n      sha: ${{ steps.version.outputs.sha }}\n", 1)
text = text.replace('          echo "version=$v" >> "$GITHUB_OUTPUT"\n',
                    '          echo "version=$v" >> "$GITHUB_OUTPUT"\n          echo "sha=$(git rev-parse HEAD)" >> "$GITHUB_OUTPUT"\n', 1)
text = text.replace("github.event_name != 'workflow_dispatch' || inputs.force_update", "inputs.force_update")
# The account comes from the token when its secret is not set (the Mask step).
text = text.replace("${{ secrets.CLOUDFLARE_ACCOUNT_ID }}", "${{ secrets.CLOUDFLARE_ACCOUNT_ID || env.CF_ACCOUNT }}")
# Kept as secrets there, so they are masked in the public log.
text = text.replace("${{ vars.DOMESTIC_MIRROR }}", "${{ secrets.DOMESTIC_MIRROR }}").replace("${{ vars.SITE_DOMAIN }}", "${{ secrets.SITE_DOMAIN }}")
for needle in ["needs.build.outputs.sha", "RELEASE_REPO", "repository: " + PRIVATE, "sealed/"]:
    if needle not in text:
        sys.exit(f"gen-release: {needle} not found; build.yml changed shape")
sys.stdout.write(text)
