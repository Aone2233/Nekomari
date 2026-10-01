#!/usr/bin/env python3
"""Contract check for the docker workflow.

Three promises about `.github/workflows/docker.yml` are invisible in a green CI
run until they are broken, so they are asserted here:

1. **`latest` means "this version passed the full release verification".** The
   workflow is triggered by the release run, whose last job deploys the published
   artifacts as a user would. When that job fails the release already exists, so
   its version tag must still be pushed -- but `latest` must not move. v1.6.6
   shipped with `13 passed / 2 failed` and was pushed as `latest` anyway.
2. **Nothing a git ref name can carry is interpolated into a shell script.** A
   tag may contain `;`, `$`, backticks and parentheses, and `${{ ... }}` inside
   `run:` is substituted *before* the shell parses the text, so a tag name is
   command injection. Event values therefore have to arrive through `env:`.
3. **The agent image creates the container marker** that stops the agent from
   replacing itself inside a container (`agent/update/update.go`:
   `containerMarkerPath`). The published image is built from the Dockerfile named
   in the workflow's *matrix*, not from `agent/Dockerfile`, so this check reads
   the matrix instead of naming a file -- otherwise moving the marker into the
   wrong Dockerfile looks fine to every reader and every green run.

Usage:
    python3 deploy/docker-workflow-check.py [--workflow PATH] [--quiet]

Exit codes: 0 = all checks pass, 1 = a check failed, 2 = a dependency is missing.
A missing dependency is a hard failure on purpose: a check that skips itself is
indistinguishable from a check that passed, which is exactly how this class of bug
survives.

No docker and no network. Everything is static except the truth table, which runs
the workflow's own `Resolve tag` script in bash.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from dataclasses import dataclass
from pathlib import Path

# The value `github.repository` renders to. It only appears in the expected image
# name below; everything else is read out of the workflow.
EXPECTED_REPOSITORY = "Aone2233/Nekomari"

# The policy, verbatim from docs/RELEASING.md: latest requires a non-prerelease
# tag AND a verified release run. The `== 'true'` comparison is load-bearing --
# GitHub treats any non-empty string as true, and the string "false" is not empty.
EXPECTED_LATEST_ENABLE = "!contains(steps.tag.outputs.tag, '-') && steps.tag.outputs.release_verified == 'true'"

# Expressions in a `run:` script whose value a git ref name can influence.
TAINTED_EXPRESSION = re.compile(
    r"\$\{\{\s*(github\.event\b|github\.head_ref\b|github\.ref\b|github\.ref_name\b|inputs\.|steps\.)"
)


class DependencyError(Exception):
    """A tool this check needs is missing. Never skipped, always fatal."""


def load_yaml():
    try:
        import yaml  # noqa: PLC0415
    except ImportError as exc:  # pragma: no cover - exercised by removing PyYAML
        raise DependencyError(
            "PyYAML is required to read the workflow.\n"
            "      install it: python3 -m pip install pyyaml"
        ) from exc
    return yaml


def find_bash() -> str:
    candidates = [shutil.which("bash")]
    if os.name == "nt":  # Git for Windows does not always put bash on PATH.
        candidates += [
            r"C:\Program Files\Git\bin\bash.exe",
            r"C:\Program Files\Git\usr\bin\bash.exe",
        ]
    for candidate in candidates:
        if candidate and Path(candidate).exists():
            return candidate
    raise DependencyError(
        "bash is required to run the workflow's `Resolve tag` script.\n"
        "      install Git for Windows or run this on a POSIX host."
    )


# --------------------------------------------------------------------------- #
# workflow access helpers
# --------------------------------------------------------------------------- #

def steps_by_name(job: dict) -> dict:
    return {step.get("name"): step for step in job["steps"] if step.get("name")}


def normalize(expression: str) -> str:
    return " ".join(expression.split())


# --------------------------------------------------------------------------- #
# check 1: the latest policy expression
# --------------------------------------------------------------------------- #

def check_latest_policy(docker_yml: Path, workflow: dict, problems: list) -> None:
    try:
        tags = steps_by_name(workflow["jobs"]["build-and-push"])["Derive image metadata"]["with"]["tags"]
    except KeyError as exc:
        problems.append(f"{docker_yml}: no `Derive image metadata` step with a `tags:` block ({exc}); the latest policy cannot be found")
        return

    match = None
    for line in tags.splitlines():
        if "type=raw,value=latest" in line:
            match = re.search(r"enable=\$\{\{\s*(.*?)\s*\}\}", line)
            break
    if match is None:
        problems.append(
            f"{docker_yml}: no `type=raw,value=latest,enable=${{{{ ... }}}}` line found in the metadata step.\n"
            f"      expected enable={EXPECTED_LATEST_ENABLE}"
        )
        return

    actual = normalize(match.group(1))
    if actual != normalize(EXPECTED_LATEST_ENABLE):
        problems.append(
            f"{docker_yml}: the latest policy changed.\n"
            f"      expected: {EXPECTED_LATEST_ENABLE}\n"
            f"      actual:   {actual}\n"
            "      latest must require release_verified == 'true'; comparing the output\n"
            "      directly (`&& steps.tag.outputs.release_verified`) is wrong because\n"
            "      GitHub treats the non-empty string \"false\" as true."
        )


# --------------------------------------------------------------------------- #
# check 2: no ref-derived value is interpolated into a shell script
# --------------------------------------------------------------------------- #

def check_no_shell_interpolation(docker_yml: Path, workflow: dict, problems: list, notes: list) -> None:
    for job_name, job in workflow["jobs"].items():
        for step in job.get("steps", []):
            script = step.get("run")
            if not script:
                continue
            for found in TAINTED_EXPRESSION.finditer(script):
                problems.append(
                    f"{docker_yml}: step {step.get('name')!r} interpolates `{found.group(0)}...` into a `run:` script.\n"
                    "      ${{ ... }} is substituted before bash parses the text, and a git ref\n"
                    "      name may contain `;`, `$`, backticks or parentheses. Pass the value\n"
                    "      through `env:` and reference it as \"$VAR\" instead."
                )
            for found in re.findall(r"\$\{\{\s*([^}]*?)\s*\}\}", script):
                notes.append(f"{step.get('name')!r} still interpolates `${{{{ {found} }}}}` (workflow constant, not ref data)")


# --------------------------------------------------------------------------- #
# check 3: the truth table, by running the real Resolve tag script
# --------------------------------------------------------------------------- #

@dataclass(frozen=True)
class Case:
    name: str
    event: str
    conclusion: str
    tag: str
    job_runs: bool
    verified: str | None  # None when the job never runs
    latest: bool


CASES = [
    Case("workflow_run success", "workflow_run", "success", "v1.6.9", True, "true", True),
    Case("workflow_run failure", "workflow_run", "failure", "v1.6.9", True, "false", False),
    Case("workflow_run timed_out", "workflow_run", "timed_out", "v1.6.9", True, "false", False),
    Case("workflow_run action_required", "workflow_run", "action_required", "v1.6.9", True, "false", False),
    Case("workflow_run cancelled", "workflow_run", "cancelled", "v1.6.9", False, None, False),
    Case("workflow_run success prerelease", "workflow_run", "success", "v1.6.9-rc1", True, "true", False),
    Case("release published", "release", "", "v1.6.9", True, "true", True),
    Case("workflow_dispatch", "workflow_dispatch", "", "v1.6.9", True, "true", True),
]

# Atoms used by the job-level `if` and by the metadata step's enable conditions.
# Anything not listed here is a hard failure rather than an assumed "".
def expression_value(expr: str, case: Case, workflow_env: dict, matrix_row: dict) -> str:
    if expr == "github.event_name":
        return case.event
    if expr == "github.event.inputs.tag":
        return case.tag if case.event == "workflow_dispatch" else ""
    if expr == "github.event.release.tag_name":
        return case.tag if case.event == "release" else ""
    if expr == "github.event.workflow_run.head_branch":
        return case.tag if case.event == "workflow_run" else ""
    if expr == "github.event.workflow_run.conclusion":
        return case.conclusion
    if expr == "github.repository":
        return EXPECTED_REPOSITORY
    if expr.startswith("env."):
        raw = workflow_env.get(expr[4:])
        if raw is None:
            raise KeyError(expr)
        return substitute(str(raw), case, workflow_env, matrix_row)
    if expr == "matrix.image_suffix":
        return matrix_row["image_suffix"]
    if expr == "matrix.name":
        return matrix_row["name"]
    raise KeyError(expr)


def substitute(raw: str, case: Case, workflow_env: dict, matrix_row: dict) -> str:
    def replace(match: re.Match) -> str:
        return expression_value(match.group(1).strip(), case, workflow_env, matrix_row)

    return re.sub(r"\$\{\{\s*(.*?)\s*\}\}", replace, raw)


def eval_github_expression(expr: str, case: Case, verified: str) -> bool:
    """Evaluate the two boolean expressions, atom by atom.

    Only the atoms these expressions actually use are translated; anything left
    over raises, so a rewritten condition is reported instead of being quietly
    evaluated as something else.
    """
    translated = expr
    replacements = {
        "github.event_name != 'workflow_run'": case.event != "workflow_run",
        "github.event.workflow_run.conclusion != 'cancelled'": case.conclusion != "cancelled",
        "github.event.workflow_run.conclusion == 'success'": case.conclusion == "success",
        "!contains(steps.tag.outputs.tag, '-')": "-" not in case.tag,
        "steps.tag.outputs.release_verified == 'true'": verified == "true",
    }
    for atom, value in replacements.items():
        translated = translated.replace(atom, repr(value))
    if re.search(r"\$\{\{|\bgithub\.|\bsteps\.|\binputs\.", translated):
        raise ValueError(f"untranslated atom in expression: {expr}")
    translated = translated.replace("&&", " and ").replace("||", " or ")
    return bool(eval(translated, {"__builtins__": {}}, {}))


def run_resolve_tag(bash: str, step: dict, workflow_env: dict, matrix_row: dict, case: Case) -> tuple[int, dict, str]:
    """Run the workflow's own Resolve tag script for one case."""
    env = {
        "GITHUB_OUTPUT": "",
        "TRIGGER_CONCLUSION": case.conclusion,
    }
    for key, raw in (step.get("env") or {}).items():
        env[key] = substitute(str(raw), case, workflow_env, matrix_row)

    with tempfile.TemporaryDirectory() as tmp:
        output_path = Path(tmp) / "github_output"
        output_path.write_text("", encoding="utf-8")
        script_path = Path(tmp) / "step.sh"
        # The script is written as-is: it must not need any substitution of its own.
        script_path.write_text(step["run"], encoding="utf-8", newline="\n")
        env["GITHUB_OUTPUT"] = str(output_path)
        # The workflow scripts are ASCII today, but a GBK/CP1252 console locale must
        # not be able to crash the check on a stray byte in the step's output.
        proc = subprocess.run(
            [bash, str(script_path)],
            env=env,
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
        )
        outputs = {}
        for line in output_path.read_text(encoding="utf-8").splitlines():
            if "=" in line:
                key, value = line.split("=", 1)
                outputs[key] = value
    return proc.returncode, outputs, (proc.stdout or "") + (proc.stderr or "")


def check_truth_table(docker_yml: Path, workflow: dict, bash: str, quiet: bool, problems: list) -> list:
    job = workflow["jobs"]["build-and-push"]
    workflow_env = workflow.get("env") or {}
    matrix_rows = job["strategy"]["matrix"]["include"]
    try:
        step = steps_by_name(job)["Resolve tag"]
    except KeyError:
        problems.append(f"{docker_yml}: no `Resolve tag` step; the truth table cannot be produced")
        return []

    unknown = object()
    rows = []
    for case in CASES:
        job_runs = eval_github_expression(job["if"], case, "true")
        matrix_row = matrix_rows[0]
        if job_runs:
            code, outputs, output_text = run_resolve_tag(bash, step, workflow_env, matrix_row, case)
            verified = outputs.get("release_verified", unknown)
            tag = outputs.get("tag", unknown)
            image = outputs.get("image", unknown)
            if code != 0:
                problems.append(f"{docker_yml}: Resolve tag failed (exit {code}) for {case.name}: {output_text.strip()}")
            if tag not in (unknown, case.tag):
                problems.append(f"{docker_yml}: Resolve tag wrote tag={tag!r} for {case.name}, want {case.tag!r}")
            if case.tag.startswith("v") and image not in (unknown,):
                expected_image = f"ghcr.io/{EXPECTED_REPOSITORY.lower()}{matrix_row['image_suffix']}"
                if image != expected_image:
                    problems.append(f"{docker_yml}: image={image!r} for {case.name}, want {expected_image!r}")
        else:
            verified, tag, image = None, None, None

        if verified is not unknown and verified != case.verified:
            problems.append(
                f"{docker_yml}: release_verified={verified!r} for {case.name}, want {case.verified!r}"
            )
        if job_runs != case.job_runs:
            problems.append(f"{docker_yml}: job runs={job_runs} for {case.name}, want {case.job_runs}")

        version_pushed = job_runs
        latest_pushed = job_runs and eval_github_expression(
            EXPECTED_LATEST_ENABLE, case, verified if isinstance(verified, str) else "false"
        )
        if latest_pushed != case.latest:
            problems.append(f"{docker_yml}: latest pushed={latest_pushed} for {case.name}, want {case.latest}")
        rows.append((case, job_runs, verified, version_pushed, latest_pushed))

    # The rejected shape: a tag that is not a release tag must fail loudly.
    bad = Case("rejected tag shape", "workflow_run", "success", "1.6.9", True, "true", False)
    code, _, output_text = run_resolve_tag(bash, step, workflow_env, matrix_rows[0], bad)
    if code == 0:
        problems.append(f"{docker_yml}: tag {bad.tag!r} was accepted; the `v*` check is gone")
    elif "does not look like a release tag" not in output_text:
        problems.append(f"{docker_yml}: the rejection of {bad.tag!r} lost its message: {output_text.strip()!r}")

    # The agent row exercises the image suffix in the same script.
    agent_row = next((r for r in matrix_rows if r.get("package") == "nekomari-agent"), None)
    if agent_row is not None:
        good = CASES[0]
        code, outputs, output_text = run_resolve_tag(bash, step, workflow_env, agent_row, good)
        expected = f"ghcr.io/{EXPECTED_REPOSITORY.lower()}{agent_row['image_suffix']}"
        if code != 0 or outputs.get("image") != expected:
            problems.append(
                f"{docker_yml}: agent row produced image={outputs.get('image')!r} (exit {code}), want {expected!r}"
            )

    if not quiet:
        print("truth table (every `release_verified` value below came from running the")
        print("workflow's own Resolve tag script in bash):")
        print()
        header = ("case", "job runs", "release_verified", "version tag", "latest")
        table = [
            (
                case.name,
                "runs" if job_runs else "SKIPPED",
                verified if isinstance(verified, str) else "-",
                "pushed" if version_pushed else "not pushed",
                "pushed" if latest_pushed else "not pushed",
            )
            for case, job_runs, verified, version_pushed, latest_pushed in rows
        ]
        widths = [max(len(str(r[i])) for r in [header] + table) for i in range(len(header))]
        for index, row in enumerate([header] + table):
            print("  ".join(str(cell).ljust(widths[i]) for i, cell in enumerate(row)))
            if index == 0:
                print("  ".join("-" * width for width in widths))
        print()
        print("note: `version tag` assumes the job reaches `Build and push`. The image smoke")
        print("test runs earlier in the same job, so a failing smoke test pushes nothing.")
        print()
    return rows


# --------------------------------------------------------------------------- #
# check 4: the Dockerfile the workflow uses creates the container marker
# --------------------------------------------------------------------------- #

def read_marker_path(update_go: Path, problems: list) -> str | None:
    if not update_go.is_file():
        problems.append(f"{update_go}: missing; the container-marker contract cannot be read")
        return None
    text = update_go.read_text(encoding="utf-8")
    match = re.search(r'containerMarkerPath\s*=\s*"([^"]+)"', text)
    if match is None:
        problems.append(
            f"{update_go}: no `containerMarkerPath = \"...\"` assignment; the agent's container\n"
            "      detection moved, so this check no longer knows what the image must create."
        )
        return None
    if "os.Stat(containerMarkerPath)" not in text:
        problems.append(
            f"{update_go}: containerMarkerPath exists but is not stat'ed by isContainerAgent();\n"
            "      the marker would no longer stop the agent from updating itself."
        )
    return match.group(1)


def touch_targets(dockerfile: Path) -> tuple[bool, list, str | None]:
    """Every `touch` invocation in the Dockerfile: (any, [(line, text, targets)], error)."""
    found: list = []
    error = None
    for number, raw in enumerate(dockerfile.read_text(encoding="utf-8").splitlines(), 1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        body = line[4:].strip() if line.startswith("RUN ") else line
        if body.startswith("["):
            try:
                argv = json.loads(body)
            except json.JSONDecodeError as exc:
                error = error or f"line {number}: cannot parse RUN exec form: {exc}"
                continue
            if isinstance(argv, list) and argv and Path(str(argv[0])).name == "touch":
                found.append((number, line, [str(a) for a in argv[1:]]))
            continue
        for segment in re.split(r"&&|;", body):
            tokens = segment.strip().split()
            if tokens and Path(tokens[0]).name == "touch":
                found.append((number, line, tokens[1:]))
    return bool(found), found, error


def check_container_marker(root: Path, docker_yml: Path, workflow: dict, problems: list) -> None:
    matrix_rows = workflow["jobs"]["build-and-push"]["strategy"]["matrix"]["include"]
    agents = [r for r in matrix_rows if r.get("package") == "nekomari-agent"]
    panels = [r for r in matrix_rows if r.get("package") == "nekomari"]
    if len(agents) != 1:
        problems.append(f"{docker_yml}: want exactly one matrix row with package 'nekomari-agent', found {len(agents)}")
        return
    if len(panels) != 1:
        problems.append(f"{docker_yml}: want exactly one matrix row with package 'nekomari', found {len(panels)}")
        return

    for label, row in (("panel", panels[0]), ("agent", agents[0])):
        if not (root / row["dockerfile"]).is_file():
            problems.append(f"{docker_yml}: the {label} row builds from {row['dockerfile']!r}, which does not exist")

    marker = read_marker_path(root / "agent" / "update" / "update.go", problems)
    if marker is None:
        return

    dockerfile = root / agents[0]["dockerfile"]
    if not dockerfile.is_file():
        return
    found, invocations, error = touch_targets(dockerfile)
    if error:
        problems.append(f"{dockerfile}: {error}")
    if any(marker in targets for _, _, targets in invocations):
        return

    if invocations:
        detail = "; ".join(f"line {number}: {text}" for number, text, _ in invocations)
        problems.append(
            f"{dockerfile}: builds the agent image ({docker_yml} matrix row 'nekomari-agent')\n"
            f"      but never touches {marker!r}.\n"
            f"      touch lines found: {detail}\n"
            "      Without it a hand-started probe replaces itself on the next release and\n"
            "      exits 42. agent/Dockerfile is NOT the published image; this row is."
        )
    else:
        problems.append(
            f"{dockerfile}: builds the agent image but contains no `touch` at all;\n"
            f"      expected a line creating {marker!r} (RUN touch {marker})."
        )


# --------------------------------------------------------------------------- #

def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--workflow", type=Path, default=None, help="path to docker.yml (default: <repo>/.github/workflows/docker.yml)")
    parser.add_argument("--quiet", action="store_true", help="print only failures")
    args = parser.parse_args()

    root = Path(__file__).resolve().parent.parent
    docker_yml = args.workflow or root / ".github" / "workflows" / "docker.yml"

    try:
        yaml = load_yaml()
        bash = find_bash()
    except DependencyError as exc:
        print(f"FAIL: {exc}", file=sys.stderr)
        return 2

    if not docker_yml.is_file():
        print(f"FAIL: {docker_yml} does not exist", file=sys.stderr)
        return 1
    workflow = yaml.safe_load(docker_yml.read_text(encoding="utf-8"))

    problems: list = []
    notes: list = []

    check_latest_policy(docker_yml, workflow, problems)
    check_no_shell_interpolation(docker_yml, workflow, problems, notes)
    rows = check_truth_table(docker_yml, workflow, bash, args.quiet, problems)
    check_container_marker(root, docker_yml, workflow, problems)

    if not args.quiet:
        print(f"workflow: {docker_yml.relative_to(root) if root in docker_yml.parents else docker_yml}")
        print(f"latest policy: enable={EXPECTED_LATEST_ENABLE}")
        print(f"truth table rows checked: {len(rows)}")
        if notes:
            print()
            print("interpolations left in `run:` scripts (workflow constants, not ref data):")
            for note in notes:
                print(f"  - {note}")

    if problems:
        print(file=sys.stderr)
        for problem in problems:
            print(f"FAIL: {problem}", file=sys.stderr)
        return 1

    if not args.quiet:
        print()
        print("all docker workflow contract checks passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
