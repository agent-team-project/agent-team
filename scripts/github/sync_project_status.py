#!/usr/bin/env python3
"""Sync GitHub Projects v2 issue-card Status from issue and PR state.

The tool is intentionally stdlib-only so it can run in GitHub Actions and local
operator shells without installing dependencies.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from typing import Any

GRAPHQL_URL = "https://api.github.com/graphql"
REST_URL = "https://api.github.com"

CLOSING_BLOCK_RE = re.compile(
    r"\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\s+([^\n]+)",
    re.IGNORECASE,
)
ISSUE_REF_RE = re.compile(
    r"(?:https://github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/issues/(\d+))"
    r"|(?:([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)#(\d+))"
    r"|(?:#(\d+))"
)


class GitHubError(RuntimeError):
    """Raised for GitHub API failures."""


@dataclass(frozen=True)
class Repo:
    owner: str
    name: str

    @classmethod
    def parse(cls, value: str) -> "Repo":
        if "/" not in value:
            raise ValueError(f"repo must be owner/name, got {value!r}")
        owner, name = value.split("/", 1)
        if not owner or not name:
            raise ValueError(f"repo must be owner/name, got {value!r}")
        return cls(owner=owner, name=name)

    @property
    def full_name(self) -> str:
        return f"{self.owner}/{self.name}"

    def matches(self, owner: str | None, name: str | None) -> bool:
        return (
            (owner or "").lower() == self.owner.lower()
            and (name or "").lower() == self.name.lower()
        )


@dataclass(frozen=True)
class ProjectStatusConfig:
    project_id: str
    project_title: str
    project_url: str
    status_field_id: str
    options: dict[str, str]


@dataclass(frozen=True)
class StatusNames:
    todo: str
    in_progress: str
    done: str
    agent_column: str


def request_json(
    method: str,
    url: str,
    token: str,
    payload: dict[str, Any] | None = None,
) -> tuple[dict[str, Any] | list[Any], dict[str, str]]:
    data = None
    headers = {
        "Accept": "application/vnd.github+json",
        "Authorization": f"Bearer {token}",
        "Content-Type": "application/json",
        "X-GitHub-Api-Version": "2022-11-28",
    }
    if payload is not None:
        data = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            body = resp.read().decode("utf-8")
            parsed: dict[str, Any] | list[Any]
            parsed = json.loads(body) if body else {}
            return parsed, dict(resp.headers)
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")
        raise GitHubError(f"{method} {url} failed: HTTP {exc.code}: {detail}") from exc
    except urllib.error.URLError as exc:
        raise GitHubError(f"{method} {url} failed: {exc.reason}") from exc


def graphql(
    token: str,
    query: str,
    variables: dict[str, Any],
    *,
    allow_not_found: bool = False,
) -> dict[str, Any] | None:
    payload = {"query": query, "variables": variables}
    parsed, _headers = request_json("POST", GRAPHQL_URL, token, payload)
    if not isinstance(parsed, dict):
        raise GitHubError("GraphQL response was not an object")
    errors = parsed.get("errors")
    if errors:
        if allow_not_found and all(err.get("type") == "NOT_FOUND" for err in errors):
            return None
        messages = "; ".join(err.get("message", str(err)) for err in errors)
        raise GitHubError(f"GraphQL error: {messages}")
    data = parsed.get("data")
    if not isinstance(data, dict):
        raise GitHubError("GraphQL response did not contain data")
    return data


def rest(
    token: str,
    path: str,
    *,
    method: str = "GET",
    payload: dict[str, Any] | None = None,
    paginate: bool = False,
) -> Any:
    url = urllib.parse.urljoin(f"{REST_URL}/", path.lstrip("/"))
    if not paginate:
        parsed, _headers = request_json(method, url, token, payload)
        return parsed

    values: list[Any] = []
    while url:
        parsed, headers = request_json(method, url, token, payload)
        if not isinstance(parsed, list):
            raise GitHubError(f"paginated REST response for {path} was not a list")
        values.extend(parsed)
        url = next_link(headers.get("Link", ""))
    return values


def next_link(link_header: str) -> str:
    for part in link_header.split(","):
        url_part, _, rel_part = part.partition(";")
        if 'rel="next"' in rel_part:
            return url_part.strip()[1:-1]
    return ""


def token_from_env() -> str:
    for name in ("PROJECTS_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"):
        value = os.environ.get(name)
        if value:
            return value
    raise GitHubError("set PROJECTS_TOKEN, GH_TOKEN, or GITHUB_TOKEN")


def load_project(
    token: str,
    owner: str,
    number: int,
    owner_kind: str,
    status_field_name: str,
) -> ProjectStatusConfig:
    kinds = [owner_kind] if owner_kind != "auto" else ["user", "organization"]
    last_error: str | None = None
    for kind in kinds:
        root_field = "user" if kind == "user" else "organization"
        query = f"""
        query($owner:String!, $number:Int!) {{
          {root_field}(login:$owner) {{
            projectV2(number:$number) {{
              id
              title
              url
              fields(first:100) {{
                nodes {{
                  __typename
                  ... on ProjectV2FieldCommon {{
                    id
                    name
                    dataType
                  }}
                  ... on ProjectV2SingleSelectField {{
                    id
                    name
                    dataType
                    options {{
                      id
                      name
                    }}
                  }}
                }}
              }}
            }}
          }}
        }}
        """
        data = graphql(
            token,
            query,
            {"owner": owner, "number": number},
            allow_not_found=owner_kind == "auto",
        )
        if data is None:
            last_error = f"{kind} {owner!r} was not found"
            continue
        project = ((data.get(root_field) or {}).get("projectV2"))
        if not project:
            last_error = f"{kind} {owner!r} has no project #{number}"
            continue
        status_field = None
        for field in project["fields"]["nodes"]:
            if field and field.get("name") == status_field_name:
                status_field = field
                break
        if not status_field:
            raise GitHubError(
                f"project #{number} does not have a field named {status_field_name!r}"
            )
        if status_field.get("__typename") != "ProjectV2SingleSelectField":
            raise GitHubError(f"field {status_field_name!r} is not a single-select field")
        options = {option["name"]: option["id"] for option in status_field.get("options", [])}
        return ProjectStatusConfig(
            project_id=project["id"],
            project_title=project["title"],
            project_url=project["url"],
            status_field_id=status_field["id"],
            options=options,
        )
    raise GitHubError(last_error or f"could not resolve project owner {owner!r}")


def require_options(project: ProjectStatusConfig, status_names: StatusNames) -> None:
    missing = [
        name
        for name in (status_names.todo, status_names.in_progress, status_names.done)
        if name not in project.options
    ]
    if missing:
        available = ", ".join(sorted(project.options))
        raise GitHubError(f"missing Status option(s): {', '.join(missing)}; have: {available}")


def fetch_issue(token: str, repo: Repo, number: int) -> dict[str, Any]:
    issue = rest(token, f"/repos/{repo.full_name}/issues/{number}")
    if not isinstance(issue, dict):
        raise GitHubError(f"issue #{number} response was not an object")
    if issue.get("pull_request"):
        raise GitHubError(f"#{number} is a pull request, not an issue")
    return issue


def fetch_pull_request(token: str, repo: Repo, number: int) -> dict[str, Any]:
    pr = rest(token, f"/repos/{repo.full_name}/pulls/{number}")
    if not isinstance(pr, dict):
        raise GitHubError(f"pull request #{number} response was not an object")
    return pr


def fetch_issue_timeline(token: str, repo: Repo, number: int) -> list[dict[str, Any]]:
    events = rest(
        token,
        f"/repos/{repo.full_name}/issues/{number}/timeline?per_page=100",
        paginate=True,
    )
    if not isinstance(events, list):
        raise GitHubError(f"issue #{number} timeline was not a list")
    return [event for event in events if isinstance(event, dict)]


def closing_issue_numbers(body: str, repo: Repo) -> list[int]:
    numbers: set[int] = set()
    for block in CLOSING_BLOCK_RE.finditer(body or ""):
        tail = block.group(1)
        for ref in ISSUE_REF_RE.finditer(tail):
            url_owner, url_repo, url_number = ref.group(1), ref.group(2), ref.group(3)
            short_owner, short_repo, short_number = ref.group(4), ref.group(5), ref.group(6)
            local_number = ref.group(7)
            if url_number and repo.matches(url_owner, url_repo):
                numbers.add(int(url_number))
            elif short_number and repo.matches(short_owner, short_repo):
                numbers.add(int(short_number))
            elif local_number:
                numbers.add(int(local_number))
    return sorted(numbers)


def body_closes_issue(body: str, repo: Repo, number: int) -> bool:
    return number in closing_issue_numbers(body, repo)


def status_from_issue_state(
    issue: dict[str, Any],
    timeline: list[dict[str, Any]],
    repo: Repo,
    status_names: StatusNames,
) -> str:
    if issue.get("state") == "closed":
        return status_names.done

    number = int(issue["number"])
    has_open_closing_pr = False
    for event in timeline:
        if event.get("event") != "cross-referenced":
            continue
        source_issue = ((event.get("source") or {}).get("issue") or {})
        if not isinstance(source_issue, dict) or not source_issue.get("pull_request"):
            continue
        source_repo = (((source_issue.get("repository") or {}).get("full_name")) or "")
        if source_repo and source_repo.lower() != repo.full_name.lower():
            continue
        if not body_closes_issue(source_issue.get("body") or "", repo, number):
            continue
        pull_ref = source_issue.get("pull_request") or {}
        if pull_ref.get("merged_at"):
            return status_names.done
        if source_issue.get("state") == "open":
            has_open_closing_pr = True

    if has_open_closing_pr:
        return status_names.in_progress
    return status_names.todo


def find_issue_project_item(
    token: str,
    repo: Repo,
    issue_number: int,
    project_id: str,
    status_field_id: str,
) -> tuple[str | None, str | None]:
    query = """
    query($owner:String!, $repo:String!, $number:Int!) {
      repository(owner:$owner, name:$repo) {
        issue(number:$number) {
          projectItems(first:50) {
            nodes {
              id
              project { id }
              fieldValues(first:50) {
                nodes {
                  __typename
                  ... on ProjectV2ItemFieldSingleSelectValue {
                    name
                    optionId
                    field { ... on ProjectV2SingleSelectField { id name } }
                  }
                }
              }
            }
          }
        }
      }
    }
    """
    data = graphql(
        token,
        query,
        {"owner": repo.owner, "repo": repo.name, "number": issue_number},
    )
    nodes = (((data.get("repository") or {}).get("issue") or {}).get("projectItems") or {}).get(
        "nodes", []
    )
    for item in nodes:
        if not item or (item.get("project") or {}).get("id") != project_id:
            continue
        return item["id"], status_value_from_item(item, status_field_id)
    return None, None


def ensure_issue_project_item(
    token: str,
    project_id: str,
    issue: dict[str, Any],
) -> str:
    mutation = """
    mutation($project:ID!, $content:ID!) {
      addProjectV2ItemById(input:{projectId:$project, contentId:$content}) {
        item { id }
      }
    }
    """
    data = graphql(token, mutation, {"project": project_id, "content": issue["node_id"]})
    return data["addProjectV2ItemById"]["item"]["id"]


def status_value_from_item(item: dict[str, Any], status_field_id: str) -> str | None:
    values = ((item.get("fieldValues") or {}).get("nodes") or [])
    for value in values:
        if not value or value.get("__typename") != "ProjectV2ItemFieldSingleSelectValue":
            continue
        field = value.get("field") or {}
        if field.get("id") == status_field_id:
            return value.get("name")
    return None


def get_item_status(
    token: str,
    item_id: str,
    status_field_id: str,
) -> str | None:
    query = """
    query($item:ID!) {
      node(id:$item) {
        ... on ProjectV2Item {
          id
          fieldValues(first:50) {
            nodes {
              __typename
              ... on ProjectV2ItemFieldSingleSelectValue {
                name
                optionId
                field { ... on ProjectV2SingleSelectField { id name } }
              }
            }
          }
        }
      }
    }
    """
    data = graphql(token, query, {"item": item_id})
    item = data.get("node") or {}
    return status_value_from_item(item, status_field_id)


def set_item_status(
    token: str,
    project: ProjectStatusConfig,
    item_id: str,
    status: str,
) -> None:
    mutation = """
    mutation($project:ID!, $item:ID!, $field:ID!, $option:String!) {
      updateProjectV2ItemFieldValue(
        input:{
          projectId:$project,
          itemId:$item,
          fieldId:$field,
          value:{singleSelectOptionId:$option}
        }
      ) {
        projectV2Item { id }
      }
    }
    """
    graphql(
        token,
        mutation,
        {
            "project": project.project_id,
            "item": item_id,
            "field": project.status_field_id,
            "option": project.options[status],
        },
    )


def sync_issue(
    token: str,
    repo: Repo,
    project: ProjectStatusConfig,
    status_names: StatusNames,
    issue_number: int,
    *,
    override_status: str | None = None,
    dry_run: bool = False,
    preserve_agent_column: bool = True,
) -> str:
    issue = fetch_issue(token, repo, issue_number)
    if issue.get("state") == "closed":
        target_status = status_names.done
    elif override_status:
        target_status = override_status
    else:
        timeline = fetch_issue_timeline(token, repo, issue_number)
        target_status = status_from_issue_state(issue, timeline, repo, status_names)

    item_id: str | None
    current_status: str | None
    if dry_run:
        item_id, current_status = find_issue_project_item(
            token, repo, issue_number, project.project_id, project.status_field_id
        )
        if not item_id:
            print(f"DRY-RUN issue #{issue_number}: would add to {project.project_title}")
    else:
        item_id = ensure_issue_project_item(token, project.project_id, issue)
        current_status = get_item_status(token, item_id, project.status_field_id)

    if (
        preserve_agent_column
        and target_status == status_names.todo
        and current_status == status_names.agent_column
    ):
        print(
            f"OK issue #{issue_number}: preserving dispatch column "
            f"{status_names.agent_column!r}"
        )
        return "preserved"

    if current_status == target_status:
        print(f"OK issue #{issue_number}: Status already {target_status!r}")
        return "unchanged"

    if dry_run:
        print(
            f"DRY-RUN issue #{issue_number}: would set Status "
            f"{current_status!r} -> {target_status!r}"
        )
        return "dry-run"

    if not item_id:
        raise GitHubError(f"issue #{issue_number} has no project item to update")
    set_item_status(token, project, item_id, target_status)
    print(f"UPDATED issue #{issue_number}: Status {current_status!r} -> {target_status!r}")
    return "updated"


def project_issue_numbers(token: str, repo: Repo, project_id: str) -> list[int]:
    query = """
    query($project:ID!, $cursor:String) {
      node(id:$project) {
        ... on ProjectV2 {
          items(first:100, after:$cursor) {
            pageInfo { hasNextPage endCursor }
            nodes {
              type
              content {
                __typename
                ... on Issue {
                  number
                  repository { nameWithOwner }
                }
              }
            }
          }
        }
      }
    }
    """
    cursor: str | None = None
    numbers: set[int] = set()
    while True:
        data = graphql(token, query, {"project": project_id, "cursor": cursor})
        items = (((data.get("node") or {}).get("items")) or {})
        for item in items.get("nodes") or []:
            content = (item or {}).get("content") or {}
            if content.get("__typename") != "Issue":
                continue
            if (content.get("repository") or {}).get("nameWithOwner", "").lower() != repo.full_name.lower():
                continue
            numbers.add(int(content["number"]))
        page_info = items.get("pageInfo") or {}
        if not page_info.get("hasNextPage"):
            break
        cursor = page_info.get("endCursor")
    return sorted(numbers)


def pull_request_overrides(
    pr: dict[str, Any],
    repo: Repo,
    status_names: StatusNames,
) -> dict[int, str | None]:
    numbers = closing_issue_numbers(pr.get("body") or "", repo)
    overrides: dict[int, str | None] = {}
    for number in numbers:
        if pr.get("merged"):
            overrides[number] = status_names.done
        elif pr.get("state") == "open":
            overrides[number] = status_names.in_progress
        else:
            overrides[number] = None
    return overrides


def targets_from_event(
    path: str,
    event_name: str,
    repo: Repo,
    status_names: StatusNames,
) -> tuple[dict[int, str | None], bool]:
    with open(path, "r", encoding="utf-8") as f:
        event = json.load(f)

    if event_name == "issues":
        issue = event.get("issue") or {}
        if issue.get("pull_request"):
            return {}, False
        action = event.get("action")
        if action == "opened":
            return {int(issue["number"]): status_names.todo}, False
        if action == "reopened":
            return {int(issue["number"]): None}, False
        if action == "closed":
            return {int(issue["number"]): status_names.done}, False
        return {}, False

    if event_name in {"pull_request", "pull_request_target"}:
        pr = event.get("pull_request") or {}
        return pull_request_overrides(pr, repo, status_names), False

    if event_name == "workflow_dispatch":
        return {}, False

    if event_name == "schedule":
        return {}, True

    return {}, False


def run_self_test() -> bool:
    repo = Repo.parse("agent-team-project/kensho")
    status_names = StatusNames(
        todo="Todo",
        in_progress="In Progress",
        done="Done",
        agent_column="Ready for Agent",
    )
    failures: list[str] = []

    body = """
    ## Summary

    Closes #12
    Fixes agent-team-project/kensho#13, https://github.com/agent-team-project/kensho/issues/14
    Advances #216
    Closes other/repo#99
    """
    if closing_issue_numbers(body, repo) != [12, 13, 14]:
        failures.append("closing_issue_numbers did not filter refs correctly")

    open_issue = {"number": 12, "state": "open"}
    closed_issue = {"number": 12, "state": "closed"}
    open_pr_event = {
        "event": "cross-referenced",
        "source": {
            "issue": {
                "number": 40,
                "state": "open",
                "body": "Closes #12",
                "repository": {"full_name": repo.full_name},
                "pull_request": {"merged_at": None},
            }
        },
    }
    merged_pr_event = {
        "event": "cross-referenced",
        "source": {
            "issue": {
                "number": 41,
                "state": "closed",
                "body": "Closes #12",
                "repository": {"full_name": repo.full_name},
                "pull_request": {"merged_at": "2026-07-08T12:00:00Z"},
            }
        },
    }
    advances_event = {
        "event": "cross-referenced",
        "source": {
            "issue": {
                "number": 42,
                "state": "open",
                "body": "Advances #12",
                "repository": {"full_name": repo.full_name},
                "pull_request": {"merged_at": None},
            }
        },
    }

    cases = [
        (open_issue, [], "Todo"),
        (closed_issue, [], "Done"),
        (open_issue, [open_pr_event], "In Progress"),
        (open_issue, [merged_pr_event], "Done"),
        (open_issue, [advances_event], "Todo"),
    ]
    for issue, timeline, expected in cases:
        actual = status_from_issue_state(issue, timeline, repo, status_names)
        if actual != expected:
            failures.append(f"expected {expected!r}, got {actual!r} for {issue} {timeline}")

    if failures:
        print("Project status sync self-test failed:", file=sys.stderr)
        for failure in failures:
            print(f"  - {failure}", file=sys.stderr)
        return False
    print("OK  Project status sync self-test")
    return True


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", default=os.environ.get("GITHUB_REPOSITORY"))
    parser.add_argument("--project-owner", default=os.environ.get("PROJECT_OWNER"))
    parser.add_argument(
        "--project-owner-kind",
        choices=("auto", "user", "organization"),
        default=os.environ.get("PROJECT_OWNER_KIND", "auto"),
    )
    parser.add_argument(
        "--project-number",
        type=int,
        default=int(os.environ.get("PROJECT_NUMBER", "1")),
    )
    parser.add_argument("--status-field", default=os.environ.get("PROJECT_STATUS_FIELD", "Status"))
    parser.add_argument("--todo-status", default=os.environ.get("PROJECT_TODO_STATUS", "Todo"))
    parser.add_argument(
        "--in-progress-status",
        default=os.environ.get("PROJECT_IN_PROGRESS_STATUS", "In Progress"),
    )
    parser.add_argument("--done-status", default=os.environ.get("PROJECT_DONE_STATUS", "Done"))
    parser.add_argument(
        "--agent-column",
        default=os.environ.get("PROJECT_AGENT_COLUMN", "Ready for Agent"),
    )
    parser.add_argument("--event-path", default=os.environ.get("GITHUB_EVENT_PATH"))
    parser.add_argument("--event-name", default=os.environ.get("GITHUB_EVENT_NAME", ""))
    parser.add_argument("--issue-number", type=int, action="append", default=[])
    parser.add_argument("--pull-request-number", type=int, action="append", default=[])
    parser.add_argument("--all-project-items", action="store_true")
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument(
        "--no-preserve-agent-column",
        action="store_true",
        help="allow Todo derivation to overwrite the dispatch column",
    )
    parser.add_argument("--self-test", action="store_true")
    return parser.parse_args(argv)


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    ok = True
    if args.self_test:
        ok = run_self_test()

    has_work = bool(
        args.event_path
        or args.issue_number
        or args.pull_request_number
        or args.all_project_items
    )
    if not has_work:
        return 0 if ok else 1
    if not args.repo:
        raise GitHubError("pass --repo or set GITHUB_REPOSITORY")

    repo = Repo.parse(args.repo)
    project_owner = args.project_owner or repo.owner
    token = token_from_env()
    status_names = StatusNames(
        todo=args.todo_status,
        in_progress=args.in_progress_status,
        done=args.done_status,
        agent_column=args.agent_column,
    )
    project = load_project(
        token,
        project_owner,
        args.project_number,
        args.project_owner_kind,
        args.status_field,
    )
    require_options(project, status_names)

    issue_targets: dict[int, str | None] = {}
    all_project_items = args.all_project_items

    if args.event_path:
        event_targets, event_all = targets_from_event(
            args.event_path,
            args.event_name,
            repo,
            status_names,
        )
        issue_targets.update(event_targets)
        all_project_items = all_project_items or event_all

    for issue_number in args.issue_number:
        issue_targets.setdefault(issue_number, None)

    for pr_number in args.pull_request_number:
        pr = fetch_pull_request(token, repo, pr_number)
        issue_targets.update(pull_request_overrides(pr, repo, status_names))

    if all_project_items:
        for issue_number in project_issue_numbers(token, repo, project.project_id):
            issue_targets.setdefault(issue_number, None)

    if not issue_targets:
        print("No issue cards to sync")
        return 0 if ok else 1

    for issue_number in sorted(issue_targets):
        sync_issue(
            token,
            repo,
            project,
            status_names,
            issue_number,
            override_status=issue_targets[issue_number],
            dry_run=args.dry_run,
            preserve_agent_column=not args.no_preserve_agent_column,
        )
    return 0 if ok else 1


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv[1:]))
    except GitHubError as exc:
        print(f"error: {exc}", file=sys.stderr)
        sys.exit(1)
