#!/bin/bash
# F6 (contract): The docs call /stress-test a "Claude Code built-in Skill"
# (Claude Code 内置的 Skill). The skill actually ships in this repository at
# .claude/skills/stress-test/ — it is a project-local skill, not built into Claude Code.
set -u
cd "$(git rev-parse --show-toplevel)"

if [ -f .claude/skills/stress-test/SKILL.md ]; then
  echo "skill ships in-repo: .claude/skills/stress-test/SKILL.md"
  if grep -qE "Claude Code's built-in|Claude Code built-in|Claude Code 内置" \
       doc/best-practice/en/09-stress-testing-and-tuning.md \
       doc/best-practice/zh/09-stress-testing-and-tuning.md; then
    echo "F6 REPRODUCED: docs describe a repo-shipped skill as Claude Code 'built-in';"
    echo "readers without this repo checked out will not find /stress-test in Claude Code."
    exit 1
  fi
fi
echo "F6 OK"
