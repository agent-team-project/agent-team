#!/bin/sh
# Build an activation-capable CLI/daemon pair with one immutable source marker.
set -eu

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"

if [ -n "$(git status --porcelain --untracked-files=all)" ]; then
  echo "scripts/build.sh: source checkout must be clean so the build identity covers every input" >&2
  exit 1
fi
ignored_inputs=$(git ls-files --others --ignored --exclude-standard -- cmd internal template embed.go go.mod go.sum)
if [ -n "$ignored_inputs" ]; then
  echo "scripts/build.sh: ignored files overlap build inputs and cannot be bound safely:" >&2
  echo "$ignored_inputs" >&2
  exit 1
fi

revision=$(git rev-parse HEAD)
case "$revision" in
  *[!0-9a-fA-F]*|'')
    echo "scripts/build.sh: unsupported git revision: $revision" >&2
    exit 1
    ;;
esac
if [ "${#revision}" -ne 40 ]; then
  echo "scripts/build.sh: expected a full 40-character git revision, got: $revision" >&2
  exit 1
fi

output_dir=${1:-bin}
mkdir -p "$output_dir"
marker="agent-team-source-v1:git:$revision:end"
identity_flag="-X github.com/agent-team-project/agent-team/internal/buildinfo.LinkedSourceIdentity=$marker"
extra_ldflags=${AGENT_TEAM_EXTRA_LDFLAGS:-}

go build -ldflags "$identity_flag $extra_ldflags" -o "$output_dir/agent-team" ./cmd/agent-team
go build -ldflags "$identity_flag $extra_ldflags" -o "$output_dir/agent-teamd" ./cmd/agent-teamd

"$output_dir/agent-team" --version
"$output_dir/agent-teamd" --version
