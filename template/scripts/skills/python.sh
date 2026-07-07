#!/usr/bin/env bash
#
# Shared Python selection helpers for bundled skill scripts.

_agent_team_resolve_python_candidate() {
    local candidate="$1"
    if [[ "$candidate" == */* ]]; then
        printf '%s\n' "$candidate"
        return 0
    fi
    command -v "$candidate" 2>/dev/null
}

_agent_team_python311_version() {
    local executable="$1"
    "$executable" -c 'import sys
print(".".join(str(part) for part in sys.version_info[:3]))
raise SystemExit(0 if sys.version_info >= (3, 11) else 1)' 2>/dev/null
}

_agent_team_python311_error() {
    local caller="$1"
    local found_path="${2:-}"
    local found_version="${3:-}"

    if [[ -n "$found_path" && -n "$found_version" ]]; then
        echo "$caller: Python 3.11+ is required for tomllib; found $found_path (Python $found_version). Install Python 3.11+ or set AGENT_TEAM_PYTHON=/path/to/python3.11." >&2
    elif [[ -n "$found_path" ]]; then
        echo "$caller: Python 3.11+ is required for tomllib; $found_path could not report a usable Python version. Install Python 3.11+ or set AGENT_TEAM_PYTHON=/path/to/python3.11." >&2
    else
        echo "$caller: Python 3.11+ is required for tomllib, but no usable Python interpreter was found. Install Python 3.11+ or set AGENT_TEAM_PYTHON=/path/to/python3.11." >&2
    fi
}

agent_team_python311() {
    local caller="${1:-skill helper}"
    local candidate=""
    local resolved=""
    local version=""
    local status=0
    local found_path=""
    local found_version=""
    local seen=":"

    if [[ -n "${AGENT_TEAM_PYTHON:-}" ]]; then
        if resolved="$(_agent_team_resolve_python_candidate "$AGENT_TEAM_PYTHON")" && [[ -x "$resolved" ]]; then
            version="$(_agent_team_python311_version "$resolved")"
            status=$?
            if [[ "$status" -eq 0 ]]; then
                printf '%s\n' "$resolved"
                return 0
            fi
            _agent_team_python311_error "$caller" "$resolved" "$version"
        else
            _agent_team_python311_error "$caller" "${resolved:-$AGENT_TEAM_PYTHON}" ""
        fi
        return 1
    fi

    local candidates=()
    local path_dirs=()
    local path_dir=""
    if [[ -n "${PATH:-}" ]]; then
        IFS=':' read -r -a path_dirs <<< "$PATH"
        for path_dir in "${path_dirs[@]}"; do
            [[ -n "$path_dir" ]] || path_dir="."
            candidates+=(
                "$path_dir/python3.13"
                "$path_dir/python3.12"
                "$path_dir/python3.11"
                "$path_dir/python3"
            )
        done
    fi
    candidates+=(
        /opt/homebrew/bin/python3.13
        /opt/homebrew/bin/python3.12
        /opt/homebrew/bin/python3.11
        /opt/homebrew/bin/python3
        /usr/local/bin/python3.13
        /usr/local/bin/python3.12
        /usr/local/bin/python3.11
        /usr/local/bin/python3
        /usr/bin/python3
    )

    for candidate in "${candidates[@]}"; do
        [[ -n "$candidate" ]] || continue
        if ! resolved="$(_agent_team_resolve_python_candidate "$candidate")"; then
            continue
        fi
        [[ -x "$resolved" ]] || continue
        case "$seen" in
            *":$resolved:"*) continue ;;
        esac
        seen="${seen}${resolved}:"

        version="$(_agent_team_python311_version "$resolved")"
        status=$?
        if [[ "$status" -eq 0 ]]; then
            printf '%s\n' "$resolved"
            return 0
        fi
        if [[ -z "$found_path" && -n "$version" ]]; then
            found_path="$resolved"
            found_version="$version"
        fi
    done

    _agent_team_python311_error "$caller" "$found_path" "$found_version"
    return 1
}
