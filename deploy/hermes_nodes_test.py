"""Offline Hermes contract checks: throwaway keys, no live Panel or credentials."""

import base64
import contextlib
import io
import json
import os
from pathlib import Path
import stat
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from unittest.mock import patch
import urllib.error
import urllib.request
import warnings

import hermes_nodes as client


KEY = "nk_" + base64.urlsafe_b64encode(bytes(range(32))).decode().rstrip("=")
NODE = "38eb3045-1651-48da-9077-2fd44e2750d0"
OTHER = "e963afc9-e60a-41ca-a66c-9a68b40fe669"
ORIGIN = "https://panel.example.test"
VERSION = {"status": "success", "data": {"version": "v1.7.0", "hash": "fixture"}}
ROW = {"uuid": NODE, "name": "fixture", "group": "test", "region": "", "hidden": False,
       "token": "SYNTHETIC-AGENT-SECRET", "remark": "private", "ipv4": "192.0.2.1"}


class FakeResponse(io.BytesIO):
    def __init__(self, body, status=200):
        super().__init__(json.dumps(body).encode())
        self.status = status


class FakeOpener:
    def __init__(self, responses):
        self.responses = list(responses)
        self.requests = []

    def open(self, request, timeout):
        self.requests.append(request)
        if not self.responses:
            raise AssertionError("unexpected/retried request")
        result = self.responses.pop(0)
        if isinstance(result, Exception):
            raise result
        return FakeResponse(result)


class HermesNodesTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.home = Path(self.temp.name)
        self.credential = self.home / "private/credential.json"

    def configure_file(self):
        client.save_credential(self.credential, ORIGIN, KEY)

    def invoke(self, args, responses=()):
        opener = FakeOpener(responses)
        output, errors = io.StringIO(), io.StringIO()
        with patch.object(client, "new_opener", return_value=opener), contextlib.redirect_stdout(output), contextlib.redirect_stderr(errors):
            code = client.main(["--credential-file", str(self.credential), *args])
        for value in (KEY, ROW["token"], "SYNTHETIC-NETWORK-SECRET"):
            self.assertNotIn(value, output.getvalue() + errors.getvalue())
        return code, output.getvalue(), errors.getvalue(), opener

    def test_origin_allows_only_plain_https_and_no_secret_bearing_urls(self):
        self.assertEqual(client.panel_origin("https://Panel.Example.Test:443/"), ORIGIN)
        for value in ("http://panel.test", "https://user:secret@panel.test", ORIGIN + "/api",
                      ORIGIN + "?key=secret", ORIGIN + "#secret", "https://panel.test\\evil",
                      "https://panel.test\n", "https://panel.test:broken", "https://panel.test:",
                      "https://panel.test?", "https://panel.test#", "https://panel.test:0"):
            with self.subTest(value=value), self.assertRaises(client.ClientError):
                client.panel_origin(value)

    def test_key_requires_canonical_new_scoped_format_and_never_uses_environment(self):
        self.assertEqual(client.validate_key(KEY), KEY)
        for value in ("old-global-test-key", KEY + "\n", "nk_" + "a" * 43, None, 123):
            with self.subTest(value=value), self.assertRaises(client.ClientError):
                client.validate_key(value)
        with patch.dict(os.environ, {"NEKOMARI_API_KEY": KEY, "KOMARI_API_KEY": KEY}):
            code, _, _, opener = self.invoke(["list"])
        self.assertEqual(code, 1)
        self.assertEqual(len(opener.requests), 0)

    def test_credential_publication_is_private_exclusive_and_cleans_failed_staging(self):
        self.configure_file()
        self.assertEqual(client.load_credential(str(self.credential)), (ORIGIN, KEY))
        self.assertEqual(stat.S_IMODE(self.credential.stat().st_mode), 0o600)
        self.assertEqual(stat.S_IMODE(self.credential.parent.stat().st_mode), 0o700)
        with self.assertRaises(FileExistsError):
            client.save_credential(self.credential, ORIGIN, KEY)
        self.assertEqual(list(self.credential.parent.iterdir()), [self.credential])
        self.credential.unlink()
        with patch.object(client.os, "link", side_effect=OSError("fixture failure")), self.assertRaises(OSError):
            client.save_credential(self.credential, ORIGIN, KEY)
        self.assertEqual(list(self.credential.parent.iterdir()), [])

    def test_credentials_refuse_symlinks_hardlinks_open_modes_and_nonregular_files(self):
        self.configure_file()
        self.credential.chmod(0o644)
        with self.assertRaises(client.ClientError):
            client.load_credential(str(self.credential))
        self.credential.chmod(0o600)
        os.link(self.credential, self.credential.parent / "hardlink")
        with self.assertRaises(client.ClientError):
            client.load_credential(str(self.credential))
        (self.credential.parent / "hardlink").unlink()
        alias = self.home / "alias"
        alias.symlink_to(self.credential.parent, target_is_directory=True)
        with self.assertRaises(client.ClientError):
            client.load_credential(str(alias / self.credential.name))
        link = self.credential.parent / "link"
        link.symlink_to(self.credential)
        with self.assertRaises(client.ClientError):
            client.save_credential(link, ORIGIN, KEY)
        self.credential.unlink()
        os.mkfifo(self.credential, 0o600)
        with self.assertRaises(client.ClientError):
            client.load_credential(str(self.credential))

    def test_credential_schema_size_and_parent_permissions_are_checked(self):
        self.configure_file()
        for raw in (b"x" * 4097, b"[]", b'{"format":"unexpected"}', b"not-json"):
            self.credential.write_bytes(raw)
            code, _, _, opener = self.invoke(["check"])
            self.assertEqual(code, 1)
            self.assertFalse(opener.requests)
        self.credential.parent.chmod(0o755)
        with self.assertRaises(client.ClientError):
            client.load_credential(str(self.credential))
        with self.assertRaises(client.ClientError):
            client.save_credential(self.credential.parent / "another", ORIGIN, KEY)

    def test_old_prerelease_unknown_or_future_major_refused_before_authentication(self):
        self.configure_file()
        for version in ("v1.6.29", "v1.7.0-rc1", "v1.07.0", "v1.7.01", "v2.0.0", "unknown", None):
            with self.subTest(version=version):
                code, _, _, opener = self.invoke(["add", "--name", "fixture"],
                                                [{"status": "success", "data": {"version": version}}])
                self.assertEqual(code, 1)
                self.assertEqual(len(opener.requests), 1)
                self.assertIsNone(opener.requests[0].get_header("Authorization"))
        for version in ("v1.7.0", "v1.8.1"):
            panel = client.Panel(ORIGIN, KEY, FakeOpener([{"status": "success", "data": {"version": version}}]))
            self.assertEqual(panel.require_scoped_version(), version)

    def test_list_get_and_check_follow_raw_rest_shapes_and_only_display_metadata(self):
        self.configure_file()
        for args, response in ((["list"], [ROW]), (["get", NODE], ROW), (["check"], [ROW])):
            with self.subTest(args=args):
                code, output, _, opener = self.invoke(args, [VERSION, response])
                self.assertEqual(code, 0)
                parsed = json.loads(output)
                if args == ["check"]:
                    self.assertEqual(parsed["nodes"], 1)
                    self.assertFalse(parsed["writes_tested"])
                else:
                    shown = parsed[0] if isinstance(parsed, list) else parsed
                    self.assertEqual(set(shown), set(client.DISPLAY_FIELDS))
                self.assertIsNone(opener.requests[0].get_header("Authorization"))
                self.assertEqual(opener.requests[1].get_header("Authorization"), "Bearer " + KEY)
                self.assertTrue(all(r.get_header("Cookie") is None for r in opener.requests))
                self.assertTrue(all(KEY not in r.full_url for r in opener.requests))
        for row in ({**ROW, "name": {"token": ROW["token"]}}, {**ROW, "hidden": "false"}):
            code, _, _, opener = self.invoke(["get", NODE], [VERSION, row])
            self.assertEqual(code, 1)
            self.assertEqual(len(opener.requests), 2)

    def test_add_accepts_flat_uuid_and_discards_any_agent_token(self):
        self.configure_file()
        code, output, _, opener = self.invoke(["add", "--name", "fixture"],
                                             [VERSION, {"status": "success", "uuid": NODE, "token": ROW["token"]}])
        self.assertEqual(code, 0)
        self.assertEqual(json.loads(output)["uuid"], NODE)
        self.assertEqual(opener.requests[-1].method, "POST")
        self.assertEqual(json.loads(opener.requests[-1].data), {"name": "fixture"})
        code, _, errors, opener = self.invoke(["add", "--name", "fixture"],
                                             [VERSION, {"status": "success", "result": {"uuid": NODE}}])
        self.assertEqual(code, 1)
        self.assertIn("inspect node state before retrying", errors)
        self.assertEqual(len(opener.requests), 2)

    def test_edit_uses_allowlist_and_never_replays_credential_or_report_fields(self):
        self.configure_file()
        patchfile = self.home / "patch.json"
        patchfile.write_text(json.dumps({"name": "changed", "hidden": True, "region": "test"}))
        code, _, _, opener = self.invoke(["edit", NODE, "--patch-file", str(patchfile)],
                                        [VERSION, {"status": "success", "data": None}])
        self.assertEqual(code, 0)
        self.assertTrue(opener.requests[-1].full_url.endswith("/" + NODE + "/edit"))
        self.assertEqual(json.loads(opener.requests[-1].data), {"name": "changed", "hidden": True, "region": "test"})
        for raw in (b'{"token":"fixture"}', b'{"uuid":"fixture"}', b'{"version":"fixture"}',
                    b'{"cpu_usage":12}', b'{}', b'[]', b'{"price":NaN}', b"x" * (client.MAX_PATCH + 1)):
            patchfile.write_bytes(raw)
            code, _, _, opener = self.invoke(["edit", NODE, "--patch-file", str(patchfile)])
            self.assertEqual(code, 1)
            self.assertFalse(opener.requests)

    def test_remove_requires_exact_uuid_confirmation_and_only_one_mutation(self):
        self.configure_file()
        code, _, _, opener = self.invoke(["remove", NODE, "--confirm-uuid", OTHER])
        self.assertEqual(code, 1)
        self.assertFalse(opener.requests)
        code, output, _, opener = self.invoke(["remove", NODE, "--confirm-uuid", NODE],
                                             [VERSION, {"status": "success", "data": None}])
        self.assertEqual(code, 0)
        self.assertEqual(json.loads(output), {"status": "removed", "uuid": NODE})
        self.assertEqual(len(opener.requests), 2)
        self.assertEqual(json.loads(opener.requests[-1].data), {})

    def test_mutation_failures_are_uncertain_safe_and_never_automatically_retried(self):
        self.configure_file()
        errors = [urllib.error.URLError("SYNTHETIC-NETWORK-SECRET"), TimeoutError(),
                  {"status": "error", "message": ROW["token"]}, {"unexpected": ROW["token"]}]
        for error in errors:
            with self.subTest(error=type(error).__name__):
                code, _, output, opener = self.invoke(["add", "--name", "fixture"], [VERSION, error])
                self.assertEqual(code, 1)
                self.assertIn("inspect node state before retrying", output)
                self.assertEqual(len(opener.requests), 2)

    def test_http_errors_invalid_json_and_oversized_response_hide_raw_content(self):
        for failure in (urllib.error.HTTPError(ORIGIN, 403, ROW["token"], {}, io.BytesIO(b"private")),
                        urllib.error.URLError(ROW["token"])):
            with self.subTest(failure=type(failure).__name__), self.assertRaises(client.ClientError) as caught:
                client.Panel(ORIGIN, KEY, FakeOpener([failure])).list_nodes()
            self.assertNotIn(ROW["token"], str(caught.exception))
        class BadOpener:
            def __init__(self, content):
                self.content = content

            def open(self, request, timeout):
                reply = FakeResponse({})
                reply.write(self.content)
                reply.seek(0)
                return reply
        for raw, reason in ((b"x" * (client.MAX_RESPONSE + 1), "byte limit"),
                            (b"SYNTHETIC-NETWORK-SECRET", "JSON")):
            with self.assertRaisesRegex(client.ClientError, reason):
                client.Panel(ORIGIN, KEY, BadOpener(raw)).list_nodes()

    def test_real_redirect_handler_never_forwards_authorization_even_same_origin(self):
        seen = []
        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                seen.append((self.path, self.headers.get("Authorization")))
                self.send_response(302)
                self.send_header("Location", "/stolen")
                self.send_header("Content-Length", "0")
                self.end_headers()

            def log_message(self, *args):
                pass
        server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            request = urllib.request.Request("http://127.0.0.1:%d/start" % server.server_port,
                                             headers={"Authorization": "Bearer " + KEY})
            with patch.dict(os.environ, {"http_proxy": "http://127.0.0.1:1"}), self.assertRaises(urllib.error.HTTPError) as caught:
                client.new_opener().open(request, timeout=3)
            caught.exception.close()
            self.assertEqual(seen, [("/start", "Bearer " + KEY)])
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=5)

    def test_configure_requires_tty_and_checks_old_version_before_prompting(self):
        with patch.object(client.sys.stdin, "isatty", return_value=False), patch.object(client.getpass, "getpass") as prompt:
            code, _, _, opener = self.invoke(["configure"])
        self.assertEqual(code, 1)
        self.assertFalse(opener.requests)
        prompt.assert_not_called()
        with patch.object(client.sys.stdin, "isatty", return_value=True), patch.object(client.getpass, "getpass") as prompt:
            code, _, _, opener = self.invoke(["configure"], [{"status": "success", "data": {"version": "v1.6.29"}}])
        self.assertEqual(code, 1)
        prompt.assert_not_called()
        self.assertIsNone(opener.requests[0].get_header("Authorization"))

    def test_configure_validates_read_then_stores_without_echoing_or_overwrite(self):
        with patch.object(client.sys.stdin, "isatty", return_value=True), patch.object(client.getpass, "getpass", return_value=KEY):
            code, output, _, opener = self.invoke(["configure", "--panel", ORIGIN], [VERSION, [ROW]])
        self.assertEqual(code, 0)
        self.assertEqual(client.load_credential(str(self.credential)), (ORIGIN, KEY))
        self.assertIn("write grant still needs owner/live acceptance", output)
        with patch.object(client.getpass, "getpass") as prompt:
            code, _, _, opener = self.invoke(["configure"])
        self.assertEqual(code, 1)
        self.assertFalse(opener.requests)
        prompt.assert_not_called()

    def test_configure_never_falls_back_to_echoed_input_or_saves_rejected_keys(self):
        def fallback(*args):
            warnings.warn("cannot hide input", client.getpass.GetPassWarning)
            raise AssertionError("fallback continued")
        with patch.object(client.sys.stdin, "isatty", return_value=True), patch.object(client.getpass, "getpass", side_effect=fallback):
            code, _, _, opener = self.invoke(["configure"], [VERSION])
        self.assertEqual(code, 1)
        self.assertFalse(self.credential.exists())
        with patch.object(client.sys.stdin, "isatty", return_value=True), patch.object(client.getpass, "getpass", return_value=KEY):
            code, _, _, opener = self.invoke(["configure"], [VERSION, {"status": "error"}])
        self.assertEqual(code, 1)
        self.assertFalse(self.credential.exists())

    def test_cli_has_no_key_token_exec_install_or_arbitrary_route_arguments(self):
        for args in (["--api-key", KEY, "list"], ["token", NODE], ["exec", "id"], ["install"], ["get", "../token"]):
            with self.subTest(command=args[0]):
                code, _, _, opener = self.invoke(args)
                self.assertEqual(code, 1)
                self.assertFalse(opener.requests)


if __name__ == "__main__":
    unittest.main(verbosity=2)
