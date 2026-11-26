#!/usr/bin/env python3
"""Check a Aero ZIP on a disposable Panel; never touches production."""
import argparse
import hashlib
from http.cookiejar import CookieJar
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import tempfile
import time
from urllib.error import URLError
from urllib.request import HTTPCookieProcessor, ProxyHandler, Request, build_opener
from playwright.sync_api import sync_playwright


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--panel-binary', type=Path, required=True)
    parser.add_argument('--archive', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    binary = args.panel_binary.resolve()
    blob = args.archive.read_bytes()
    result = {'panel_sha256': hashlib.sha256(binary.read_bytes()).hexdigest(),
              'archive_sha256': hashlib.sha256(blob).hexdigest(), 'checks': []}
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    base = f'http://127.0.0.1:{port}'
    opener = build_opener(ProxyHandler({}), HTTPCookieProcessor(CookieJar()))
    guest = build_opener(ProxyHandler({}))

    def call(path, payload=None, client=opener, token=None):
        headers = {'Content-Type': 'application/json'}
        if token:
            headers['Authorization'] = 'Bearer ' + token
        request = Request(base + path, data=None if payload is None else json.dumps(payload).encode(), headers=headers)
        with client.open(request, timeout=20) as response:
            return json.load(response)

    def require(value, message):
        if not value:
            raise RuntimeError(message)

    with tempfile.TemporaryDirectory(prefix='nekomari-aero-check-') as temp:
        work = Path(temp)
        with (work / 'panel.log').open('wb') as log:
            process = subprocess.Popen([str(binary), '-d', str(work / 'panel.db'), 'server', '--listen', f'127.0.0.1:{port}'], cwd=work, stdout=log, stderr=subprocess.STDOUT, env={**os.environ, 'HOME': temp})
            try:
                for _ in range(200):
                    require(process.poll() is None, 'panel exited during setup')
                    try:
                        call('/api/install/status')
                        break
                    except (URLError, TimeoutError):
                        time.sleep(.1)
                else:
                    raise RuntimeError('panel readiness timeout')
                password = 'Fixture-' + secrets.token_urlsafe(24)
                require(call('/api/install/complete', {'username': 'fixture', 'password': password, 'sitename': 'Fixture', 'metric_dsn': str(work / 'metrics.db')})['status'] == 'success', 'install failed')
                for _ in range(100):
                    try:
                        call('/api/version')
                        break
                    except (URLError, TimeoutError):
                        time.sleep(.1)
                require(call('/api/login', {'username': 'fixture', 'password': password})['status'] == 'success', 'login failed')
                node = call('/api/admin/client/add', {'name': 'Theme Fixture Node'})
                info = {'cpu_name': 'Fixture CPU', 'cpu_cores': 2, 'arch': 'amd64', 'os': 'Fixture', 'kernel_version': 'test', 'mem_total': 1073741824, 'disk_total': 2147483648, 'version': 'fixture'}
                report = {'cpu': {'usage': 37.5, 'cores': 2}, 'ram': {'total': 1073741824, 'used': 536870912}, 'swap': {}, 'load': {'load1': .5}, 'disk': {'total': 2147483648, 'used': 1073741824}, 'network': {'up': 1024, 'down': 2048, 'totalUp': 5000000, 'totalDown': 7000000}, 'connections': {}, 'uptime': 123456}
                for method, params in [('agent.basicInfo', {'info': info}), ('agent.report', {'report': report})]:
                    answer = call('/api/clients/v2/rpc', {'jsonrpc': '2.0', 'id': 1, 'method': method, 'params': params}, guest, node['token'])
                    require('error' not in answer, method + ' rejected')
                init = call('/api/admin/upload/init', {'purpose': 'theme', 'size': len(blob), 'filename': 'fixture.zip'})['data']
                size, upload = init['chunk_size'], init['upload_id']
                for index in range((len(blob) + size - 1) // size):
                    boundary = 'fixture-' + secrets.token_hex(16)
                    fields = [('upload_id', upload), ('chunk_index', str(index))]
                    body = b''.join((f'--{boundary}\r\nContent-Disposition: form-data; name="{key}"\r\n\r\n{value}\r\n').encode() for key, value in fields)
                    body += (f'--{boundary}\r\nContent-Disposition: form-data; name="chunk_data"; filename="chunk"\r\nContent-Type: application/octet-stream\r\n\r\n').encode() + blob[index * size:(index + 1) * size] + f'\r\n--{boundary}--\r\n'.encode()
                    request = Request(base + '/api/admin/upload/chunk', data=body, headers={'Content-Type': 'multipart/form-data; boundary=' + boundary})
                    with opener.open(request, timeout=30) as response:
                        require(json.load(response)['status'] == 'success', 'chunk upload failed')
                merged = call('/api/admin/upload/merge', {'upload_id': upload})
                require(merged['status'] == 'success' and merged['data']['short'] == 'aero', 'unexpected theme upload result')
                require(call('/api/admin/theme/set?theme=aero')['status'] == 'success', 'theme selection failed')
                with sync_playwright() as pw:
                    browser = pw.chromium.launch(headless=True)
                    try:
                        for width in (320, 390, 560):
                            for action in ('touch', 'keyboard'):
                                context = browser.new_context(viewport={'width': width, 'height': 900}, is_mobile=True, has_touch=True, service_workers='block')
                                context.route('**/*', lambda route: route.continue_() if route.request.url.startswith(base + '/') else route.abort())
                                page = context.new_page()
                                errors = []
                                page.on('pageerror', lambda error: errors.append(str(error)[:300]))
                                check = {'width': width, 'action': action, 'passed': False}
                                try:
                                    response = page.goto(base + '/', wait_until='domcontentloaded', timeout=15000)
                                    require(response is not None and response.status == 200, 'root request failed')
                                    page.get_by_text('Theme Fixture Node', exact=False).first.wait_for(timeout=10000)
                                    link = page.locator('a.node-card').first
                                    link.scroll_into_view_if_needed()
                                    page.wait_for_timeout(800)
                                    if action == 'touch':
                                        link.tap(timeout=5000)
                                    else:
                                        link.focus()
                                        page.wait_for_timeout(200)
                                        require(page.locator('#node-peek').is_visible(), 'keyboard tooltip missing')
                                        page.keyboard.press('Enter')
                                    page.wait_for_timeout(1500)
                                    check['navigated'] = page.url == base + '/instance/' + node['uuid']
                                    check['page_errors'] = errors
                                    check['passed'] = check['navigated'] and not errors
                                except Exception as exc:
                                    check['error'] = str(exc).replace(base, 'http://localhost:<port>')[:400]
                                finally:
                                    result['checks'].append(check)
                                    context.close()
                    finally:
                        browser.close()
            finally:
                process.terminate()
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
    result['passed'] = len(result['checks']) == 6 and all(check['passed'] for check in result['checks'])
    args.output.write_text(json.dumps(result, indent=2, ensure_ascii=False) + '\n')
    print(json.dumps(result, ensure_ascii=False))
    return 0 if result['passed'] else 1

if __name__ == '__main__':
    raise SystemExit(main())
