#!/usr/bin/env python3
"""Explicit, version-gated node metadata access for Hermes; no Agent credentials."""

import argparse
import base64
import contextlib
import getpass
import http.client
import json
import os
from pathlib import Path
import re
import socket
import stat
import sys
import tempfile
import urllib.error
import urllib.parse
import urllib.request
import uuid
import warnings


FORMAT = "nekomari-hermes-v1"
DEFAULT_PANEL = "https://nekomari.orderly2233.org"
MAX_RESPONSE = 2 << 20
MAX_PATCH = 64 << 10
TIMEOUT = 20
EDIT_FIELDS = frozenset((
    "name", "group", "tags", "weight", "price", "billing_cycle", "currency",
    "expired_at", "hidden", "traffic_limit", "traffic_limit_type", "auto_renewal",
    "public_remark", "remark", "region",
))
DISPLAY_FIELDS = ("uuid", "name", "group", "region", "hidden")


class ClientError(Exception):
    pass


def panel_origin(value):
    if (not isinstance(value, str) or len(value) > 512 or not value.isascii()
            or any(ord(c) <= 32 or ord(c) == 127 for c in value)):
        raise ClientError("panel must be an HTTPS origin without credentials, path or query")
    try:
        url = urllib.parse.urlsplit(value)
        valid = (url.scheme == "https" and url.hostname and not url.username
                 and not url.password and url.path in ("", "/") and not url.query
                 and not url.fragment and re.fullmatch(r"[A-Za-z0-9.-]+", url.hostname)
                 and "@" not in url.netloc and "\\" not in value
                 and "?" not in value and "#" not in value
                 and not url.netloc.endswith(":"))
        port = url.port
        valid = valid and port != 0
    except ValueError:
        valid, port = False, None
    if not valid:
        raise ClientError("panel must be an HTTPS origin without credentials, path or query")
    return "https://" + url.hostname.lower() + (":" + str(port) if port and port != 443 else "")


def validate_key(value):
    if not isinstance(value, str) or not re.fullmatch(r"nk_[A-Za-z0-9_-]{43}", value):
        raise ClientError("a newly issued scoped API key is required; no legacy-key fallback")
    raw = base64.urlsafe_b64decode(value[3:] + "=")
    if len(raw) != 32 or base64.urlsafe_b64encode(raw).decode().rstrip("=") != value[3:]:
        raise ClientError("a canonical scoped API key is required")
    return value


def credential_path(value):
    path = Path(os.path.abspath(os.path.expanduser(value)))
    if any(p.is_symlink() for p in (path, *path.parents)):
        raise ClientError("credential paths must not traverse symlinks")
    return path


def private_parent(path, create=False):
    parent = path.parent
    if create:
        parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    info = parent.lstat()
    if (not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid()
            or info.st_mode & 0o077):
        raise ClientError("credential directory must be owned by this user with mode 0700")


def load_credential(value):
    path = credential_path(value)
    private_parent(path)
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, "rb") as file:
        info = os.fstat(file.fileno())
        if (not stat.S_ISREG(info.st_mode) or info.st_nlink != 1
                or info.st_uid != os.getuid() or info.st_mode & 0o077 or info.st_size > 4096):
            raise ClientError("credential must be a private, owned, regular 0600 file")
        raw = file.read(4097)
    if len(raw) > 4096:
        raise ClientError("credential file is too large")
    data = json.loads(raw)
    if not isinstance(data, dict) or set(data) != {"format", "panel", "key"} or data["format"] != FORMAT:
        raise ClientError("unsupported credential format")
    return panel_origin(data["panel"]), validate_key(data["key"])


def save_credential(path, origin, key):
    path = credential_path(str(path))
    private_parent(path, create=True)
    raw = json.dumps({"format": FORMAT, "panel": panel_origin(origin), "key": validate_key(key)}) + "\n"
    descriptor, temporary = tempfile.mkstemp(prefix=".credential-", dir=path.parent)
    try:
        with os.fdopen(descriptor, "w", encoding="ascii") as file:
            os.fchmod(file.fileno(), 0o600)
            file.write(raw)
            file.flush()
            os.fsync(file.fileno())
        # Exclusive publication never replaces an existing/uncertain credential.
        os.link(temporary, path)
    finally:
        Path(temporary).unlink(missing_ok=True)
    fd = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def new_opener():
    # No proxy, redirects, cookie jar, session or retry fallback for this credential.
    return urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())


class Panel:
    def __init__(self, origin, key=None, opener=None):
        self.origin = panel_origin(origin)
        self.key = validate_key(key) if key is not None else None
        self.opener = opener if opener is not None else new_opener()

    def request(self, path, *, body=None, authenticated=True):
        data = json.dumps(body, allow_nan=False).encode("utf-8") if body is not None else None
        if data is not None and len(data) > MAX_PATCH:
            raise ClientError("request payload is too large")
        headers = {"Accept": "application/json", "User-Agent": "nekomari-hermes-nodes"}
        if authenticated:
            if self.key is None:
                raise ClientError("an explicit scoped API key is required")
            headers["Authorization"] = "Bearer " + self.key
        if data is not None:
            headers["Content-Type"] = "application/json"
        req = urllib.request.Request(self.origin + path, data=data, headers=headers,
                                     method="POST" if data is not None else "GET")
        try:
            with self.opener.open(req, timeout=TIMEOUT) as response:
                if response.status != 200:
                    raise ClientError("unexpected HTTP status")
                raw = response.read(MAX_RESPONSE + 1)
                if len(raw) > MAX_RESPONSE:
                    raise ClientError("response exceeds the byte limit")
        except urllib.error.HTTPError as error:
            code = error.code
            error.close()
            raise ClientError("HTTP request rejected (status %d); response withheld" % code) from None
        except (urllib.error.URLError, http.client.HTTPException, TimeoutError, socket.timeout, OSError):
            raise ClientError("request failed; raw network diagnostics withheld") from None
        try:
            return json.loads(raw)
        except (ValueError, RecursionError):
            raise ClientError("response is not bounded valid JSON; body withheld") from None

    def require_scoped_version(self):
        result = self.request("/api/version", authenticated=False)
        data = result.get("data") if isinstance(result, dict) and result.get("status") == "success" else None
        version = data.get("version") if isinstance(data, dict) else None
        match = re.fullmatch(r"v1\.([1-9]\d{0,2})\.(0|[1-9]\d{0,5})", version) if isinstance(version, str) else None
        if match is None or int(match[1]) < 7:
            raise ClientError("requires a stable v1.7+ Panel; v1.6/prerelease/unknown major refused")
        return version

    def list_nodes(self):
        rows = self.request("/api/admin/client/list")
        if not isinstance(rows, list) or not all(isinstance(row, dict) for row in rows):
            raise ClientError("invalid node-list response; body withheld")
        return [display_node(row) for row in rows]

    def get_node(self, node):
        return display_node(self.request("/api/admin/client/" + node_uuid(node)))

    def mutate(self, path, body, *, added=False):
        try:
            result = self.request(path, body=body)
            if not isinstance(result, dict) or result.get("status") != "success":
                raise ClientError("mutation did not return a success envelope")
            return node_uuid(result.get("uuid")) if added else None
        except ClientError:
            raise ClientError("mutation not confirmed; inspect node state before retrying; response withheld") from None


def node_uuid(value):
    try:
        if not isinstance(value, str) or str(uuid.UUID(value)) != value:
            raise ValueError()
    except ValueError:
        raise ClientError("a canonical node UUID is required") from None
    return value


def display_node(row):
    if not isinstance(row, dict):
        raise ClientError("invalid node response; body withheld")
    node_uuid(row.get("uuid"))
    if (any(field in row and not isinstance(row[field], str) for field in ("name", "group", "region"))
            or ("hidden" in row and type(row["hidden"]) is not bool)):
        raise ClientError("invalid node metadata types; body withheld")
    return {field: row[field] for field in DISPLAY_FIELDS if field in row}


def read_patch(path):
    with contextlib.ExitStack() as stack:
        file = sys.stdin.buffer if path == "-" else stack.enter_context(open(path, "rb"))
        raw = file.read(MAX_PATCH + 1)
    if len(raw) > MAX_PATCH:
        raise ClientError("metadata patch exceeds the byte limit")
    patch = json.loads(raw)
    if not isinstance(patch, dict) or not patch or not set(patch) <= EDIT_FIELDS:
        raise ClientError("patch must contain only supported metadata fields; credentials/report fields refused")
    # Parsing NaN is permissive in Python; never send it to the Panel.
    json.dumps(patch, allow_nan=False)
    return patch


class SafeParser(argparse.ArgumentParser):
    def error(self, message):
        raise ClientError("invalid command/options; use --help; argument values withheld")


def parser():
    root = SafeParser(description=__doc__)
    root.add_argument("--credential-file", default=str(Path.home() / ".config/nekomari-hermes/credential.json"))
    commands = root.add_subparsers(dest="command", required=True)
    configure = commands.add_parser("configure", help="owner-only TTY prompt; verify a new key and save exclusively")
    configure.add_argument("--panel", default=DEFAULT_PANEL)
    for name in ("check", "list"):
        commands.add_parser(name)
    for name in ("get", "edit", "remove"):
        command = commands.add_parser(name)
        command.add_argument("uuid", type=node_uuid)
        if name == "edit":
            command.add_argument("--patch-file", required=True, help="JSON metadata file, or - for stdin")
        if name == "remove":
            command.add_argument("--confirm-uuid", required=True, type=node_uuid)
    add = commands.add_parser("add")
    add.add_argument("--name", required=True)
    return root


def run(args):
    if args.command == "configure":
        path = credential_path(args.credential_file)
        if path.exists():
            raise ClientError("credential already exists; refusing to replace it")
        origin = panel_origin(args.panel)
        if not sys.stdin.isatty():
            raise ClientError("configure requires an owner-operated TTY; no stdin/argv/environment secret input")
        panel = Panel(origin)
        version = panel.require_scoped_version()
        with warnings.catch_warnings():
            warnings.simplefilter("error", getpass.GetPassWarning)
            try:
                panel.key = validate_key(getpass.getpass("New Hermes scoped API key (hidden): ").strip())
            except getpass.GetPassWarning:
                raise ClientError("hidden terminal input is unavailable; no echoed-input fallback") from None
        panel.list_nodes()
        save_credential(path, origin, panel.key)
        return {"status": "configured", "panel": origin, "version": version, "credential_file": str(path),
                "verified_access": "nodes:read; write grant still needs owner/live acceptance"}

    # Validate mutation input before making even a read-only network request.
    if args.command == "add" and (not args.name.strip() or len(args.name.encode()) > 128
                                  or any(ord(c) < 32 or ord(c) == 127 for c in args.name)):
        raise ClientError("name must be 1-128 UTF-8 bytes without control characters")
    if args.command == "remove" and args.confirm_uuid != args.uuid:
        raise ClientError("deletion confirmation UUID does not match")
    patch = read_patch(args.patch_file) if args.command == "edit" else None
    origin, key = load_credential(args.credential_file)
    panel = Panel(origin, key)
    version = panel.require_scoped_version()
    if args.command == "check":
        return {"status": "read-check-passed", "panel": origin, "version": version,
                "nodes": len(panel.list_nodes()), "writes_tested": False}
    if args.command == "list":
        return panel.list_nodes()
    if args.command == "get":
        return panel.get_node(args.uuid)
    if args.command == "add":
        node = panel.mutate("/api/admin/client/add", {"name": args.name}, added=True)
        return {"status": "created", "uuid": node, "agent_token": "not requested; owner installs via UI"}
    if args.command == "edit":
        panel.mutate("/api/admin/client/" + args.uuid + "/edit", patch)
        return {"status": "updated", "uuid": args.uuid}
    panel.mutate("/api/admin/client/" + args.uuid + "/remove", {})
    return {"status": "removed", "uuid": args.uuid}


def main(argv=None):
    os.umask(0o077)
    try:
        result = run(parser().parse_args(argv))
        print(json.dumps(result, ensure_ascii=True, allow_nan=False))
    except KeyboardInterrupt:
        print("FAIL: interrupted; inspect node state before retrying a mutation", file=sys.stderr)
        return 1
    except (ClientError, OSError, ValueError, TypeError, RecursionError, EOFError) as error:
        reason = str(error) if isinstance(error, ClientError) else type(error).__name__
        print("FAIL: " + reason, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
