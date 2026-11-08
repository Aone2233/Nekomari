#!/usr/bin/env python3
"""Exercise the release's opt-in Agent staging and image assembly scripts."""

import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile

import yaml

ROOT = Path(__file__).resolve().parent.parent
RELEASE = yaml.safe_load((ROOT / ".github/workflows/release.yml").read_text())
DOCKER = yaml.safe_load((ROOT / ".github/workflows/docker.yml").read_text())


def step(job, name):
    matches = [s for s in job["steps"] if s.get("name") == name]
    assert len(matches) == 1, name
    return matches[0]


def run(script, cwd, env=None, success=True):
    result = subprocess.run(["bash", "-c", script], cwd=cwd, env=env,
                            text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    if (result.returncode == 0) != success:
        raise AssertionError(f"unexpected exit {result.returncode}:\n{result.stdout}")


names = ["komari-agent-linux-amd64", "komari-agent-linux-arm64",
         "komari-agent-windows-amd64.exe", "komari-agent-darwin-amd64",
         "komari-agent-darwin-arm64"]
release_job = RELEASE["jobs"]["release"]
stage = step(release_job, "Stage opt-in Agent assets")["run"]
steps = [s.get("name") for s in release_job["steps"]]
assert steps.index("Stage opt-in Agent assets") < steps.index("Generate SBOM")
assert steps.index("Stage opt-in Agent assets") < steps.index("Generate checksums")
assert steps.index("Stage opt-in Agent assets") < steps.index("Attest build provenance")
attest = step(release_job, "Attest build provenance")["with"]["subject-path"]
assert "dist/manual-komari-agent-*" in attest
create = step(release_job, "Create release")["with"]
assert step(release_job, "Create release")["id"] == "create_release"
assert create["draft"] is True and create["make_latest"] is False
assert create["overwrite_files"] is False and create["fail_on_unmatched_files"] is True
assert steps.index("Refuse an existing release") < steps.index("Create release")
assert steps.index("Create release") < steps.index("Validate and publish opt-in release")
publish = step(release_job, "Validate and publish opt-in release")["run"]
assert "set(names) != expected" in publish and "-F draft=false -f make_latest=false" in publish
assert step(release_job, "Validate and publish opt-in release")["env"]["RELEASE_ID"] == "${{ steps.create_release.outputs.id }}"
assert 'releases/${RELEASE_ID}' in publish and 'releases/tags/${RELEASE_TAG}" >' not in publish
verify = step(RELEASE["jobs"]["verify"], "Deploy the published artifacts and check they work")["run"]
assert "NEKOMARI_MANUAL_AGENT_ASSET=1" in verify

with tempfile.TemporaryDirectory() as tmp:
    root = Path(tmp)
    dist = root / "dist"
    dist.mkdir()
    for name in names:
        (dist / name).write_bytes(name.encode())
    stage_env = dict(os.environ, RELEASE_TAG="v1.7.30")
    run(stage, root, dict(os.environ, RELEASE_TAG="v1.7.31"), success=False)
    run(stage, root, stage_env)
    assert sorted(p.name for p in dist.iterdir()) == sorted("manual-" + n for n in names)
    # Re-running or staging a partial set fails before any publication.
    run(stage, root, stage_env, success=False)
    (dist / ("manual-" + names[-1])).rename(dist / names[-1])
    run(stage, root, stage_env, success=False)

# The tag lookup deliberately cannot see drafts. Exercise the real publication
# step with a fake API that exposes the candidate only by its numeric ID.
with tempfile.TemporaryDirectory() as tmp:
    root = Path(tmp)
    bindir = root / "bin"
    bindir.mkdir()
    gh = bindir / "gh"
    gh.write_text('''#!/usr/bin/env python3
import json, os, pathlib, sys
args = sys.argv[1:]
state = pathlib.Path(os.environ["FAKE_RELEASE_STATE"])
release = json.loads(state.read_text())
if args == ["api", "repos/test/repo/releases/17"]:
    print(json.dumps(release))
elif args == ["api", "--method", "PATCH", "repos/test/repo/releases/17", "-F", "draft=false", "-f", "make_latest=false"]:
    release["draft"] = False
    state.write_text(json.dumps(release))
elif args == ["api", "repos/test/repo/releases/tags/v1.7.30", "--jq", "[.draft,.prerelease,.tag_name] | @tsv"] and not release["draft"]:
    print("false\\tfalse\\tv1.7.30")
else:
    sys.exit(1)
''')
    gh.chmod(0o755)
    asset_names = ["manual-" + name for name in names] + [
        "nekomari-linux-amd64", "nekomari-linux-arm64", "nekomari-windows-amd64.exe",
        "SHA256SUMS.txt", "sbom.spdx.json"]
    state = root / "release.json"
    candidate = {"id": 17, "tag_name": "v1.7.30", "draft": True, "prerelease": False,
                 "assets": [{"name": name} for name in asset_names]}
    env = dict(os.environ, PATH=str(bindir) + os.pathsep + os.environ["PATH"],
               RELEASE_ID="17", RELEASE_TAG="v1.7.30", RELEASE_REPO="test/repo", FAKE_RELEASE_STATE=str(state))
    # JSON is used by the real shell and fake API; reset for each rejected case.
    state.write_text(json.dumps(candidate))
    run(publish, root, env)
    assert json.loads(state.read_text())["draft"] is False
    for field, value in (("tag_name", "v1.7.31"), ("draft", False), ("prerelease", True),
                         ("assets", candidate["assets"][:-1])):
        state.write_text(json.dumps(dict(candidate, **{field: value})))
        run(publish, root, env, success=False)
    state.write_text(json.dumps(candidate))
    run(publish, root, dict(env, RELEASE_ID="17/../../tags/v1.7.30"), success=False)

assemble = step(DOCKER["jobs"]["build-and-push"], "Assemble the build context")["run"]
assert "SHA256SUMS.txt" in assemble and "sha256sum -c" in assemble
assert "manual-komari-agent-linux-*" in assemble
metadata = step(DOCKER["jobs"]["build-and-push"], "Derive image metadata")["with"]
assert "latest=false" in metadata["flavor"].splitlines()
tags = metadata["tags"]
for line in tags.splitlines():
    if "value=latest" in line or "pattern={{major}}.{{minor}}" in line:
        assert "matrix.package == 'nekomari'" in line
policy = step(DOCKER["jobs"]["build-and-push"], "Verify manual Agent publication policy")
assert policy["if"] == "matrix.package == 'nekomari-agent'"
assert "^komari-agent-" in policy["run"]

# Run the real shell with an offline fake gh and assert its checksum failures
# cannot silently produce an image from tampered Agent bytes.
with tempfile.TemporaryDirectory() as tmp:
    root = Path(tmp)
    (root / "Dockerfile").write_text("FROM scratch\n")
    (root / "Dockerfile.agent").write_text("FROM scratch\n")
    source = root / "assets"
    source.mkdir()
    for arch in ("amd64", "arm64"):
        for prefix in ("nekomari-linux-", "manual-komari-agent-linux-"):
            name = prefix + arch
            (source / name).write_bytes(name.encode())
    (source / "SHA256SUMS.txt").write_text("".join(
        f"{hashlib.sha256(p.read_bytes()).hexdigest()}  {p.name}\n"
        for p in sorted(source.iterdir())))
    bindir = root / "bin"
    bindir.mkdir()
    gh = bindir / "gh"
    gh.write_text("""#!/usr/bin/env python3
import fnmatch, os, pathlib, shutil, sys
args = sys.argv[1:]
assert args[:2] == ['release', 'download']
pattern = args[args.index('--pattern') + 1]
dest = pathlib.Path(args[args.index('--dir') + 1])
files = [p for p in pathlib.Path(os.environ['FAKE_ASSETS']).iterdir() if fnmatch.fnmatch(p.name, pattern)]
assert files
for p in files: shutil.copyfile(p, dest / p.name)
""")
    gh.chmod(0o755)
    env = dict(os.environ, PATH=str(bindir) + os.pathsep + os.environ["PATH"],
               GH_TOKEN="fake", TAG="v1.7.30", REPO="test/repo", FAKE_ASSETS=str(source))
    for package in ("nekomari", "nekomari-agent"):
        case = root / package
        case.mkdir()
        for name in ("Dockerfile", "Dockerfile.agent"):
            (case / name).write_text("FROM scratch\n")
        run(assemble, case, dict(env, PACKAGE=package))
        if package == "nekomari-agent":
            for arch in ("amd64", "arm64"):
                assert (case / "ctx" / ("komari-agent-linux-" + arch)).read_bytes() == (
                    "manual-komari-agent-linux-" + arch).encode()
    (source / "manual-komari-agent-linux-amd64").write_bytes(b"tampered")
    bad = root / "bad"
    bad.mkdir()
    for name in ("Dockerfile", "Dockerfile.agent"):
        (bad / name).write_text("FROM scratch\n")
    run(assemble, bad, dict(env, PACKAGE="nekomari-agent"), success=False)

print("Manual Agent publication, verification, image assembly and tamper gates pass.")
