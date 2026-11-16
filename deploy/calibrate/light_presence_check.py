#!/usr/bin/env python3
"""Check locked Light presence transitions with a real, disposable Agent.

Requires Playwright Chromium. Run in a network namespace with loopback enabled:
the browser is restricted to the Panel, but the Agent can perform its own
lookups. Exit 0 only if both mobile views match independent Panel presence for
online, stopped and reconnected states. Reports omit credentials and metrics.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import tempfile
import time
from http.cookiejar import CookieJar
from urllib.error import URLError
from urllib.request import HTTPCookieProcessor, ProxyHandler, Request, build_opener
import zipfile

from playwright.sync_api import sync_playwright

ROOT = Path(__file__).resolve().parents[2]
LOCK = ROOT / 'web/api/admin/testdata/komari-market-20261108.lock.json'
NODE_NAME = 'Theme Fixture Node'


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def stop(process):
    if process is None or process.poll() is not None:
        return
    process.terminate()
    try:
        process.wait(timeout=8)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--panel-binary', type=Path, required=True)
    parser.add_argument('--agent-binary', type=Path, required=True)
    parser.add_argument('--archive', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    panel, agent, archive = (p.resolve(strict=True) for p in
                             (args.panel_binary, args.agent_binary, args.archive))
    items = json.loads(LOCK.read_text())['items']
    expected = next(item['archive_sha256'] for item in items if item['short'] == 'light')
    if digest(archive) != expected:
        parser.error('archive does not match the locked Light ZIP')
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    base = f'http://127.0.0.1:{port}'
    opener = build_opener(ProxyHandler({}), HTTPCookieProcessor(CookieJar()))

    def call(path, data=None):
        request = Request(base + path, data=None if data is None else json.dumps(data).encode(),
                          headers={'Content-Type': 'application/json'})
        with opener.open(request, timeout=10) as response:
            return json.load(response)

    result = {'panel_sha256': digest(panel), 'agent_sha256': digest(agent),
              'archive_sha256': digest(archive), 'views': [], 'status': 'incomplete'}
    panel_process = agent_process = None
    try:
        with tempfile.TemporaryDirectory(prefix='nekomari-light-presence-') as temp:
            scratch = Path(temp)
            with (scratch / 'panel.log').open('wb') as panel_log, (scratch / 'agent.log').open('wb') as agent_log:
                try:
                    panel_process = subprocess.Popen(
                        [str(panel), '-d', str(scratch / 'panel.db'), 'server', '--listen', f'127.0.0.1:{port}'],
                        cwd=scratch, stdout=panel_log, stderr=subprocess.STDOUT, env={**os.environ, 'HOME': temp})
                    for _ in range(150):
                        try:
                            call('/api/install/status')
                            break
                        except (URLError, TimeoutError):
                            time.sleep(.1)
                    else:
                        raise RuntimeError('Panel did not start')
                    password = secrets.token_urlsafe(32) + 'aA1!'
                    if call('/api/install/complete', {'username': 'theme-fixture', 'password': password,
                            'sitename': 'Theme fixture', 'metric_dsn': str(scratch / 'metrics.db')}).get('status') != 'success':
                        raise RuntimeError('Panel setup failed')
                    for _ in range(100):
                        try:
                            call('/api/version')
                            break
                        except (URLError, TimeoutError):
                            time.sleep(.1)
                    else:
                        raise RuntimeError('Panel did not finish setup')
                    if call('/api/login', {'username': 'theme-fixture', 'password': password}).get('status') != 'success':
                        raise RuntimeError('Panel login failed')
                    node = call('/api/admin/client/add', {'name': NODE_NAME})
                    token = scratch / 'agent.token'
                    token.write_text(node['token'])
                    token.chmod(0o600)

                    def start_agent():
                        return subprocess.Popen([str(agent), '--endpoint', base, '--token-file', str(token),
                                                 '--interval', '1', '--disable-auto-update', '--disable-web-ssh'],
                                                cwd=scratch, stdout=agent_log, stderr=subprocess.STDOUT,
                                                env={**os.environ, 'HOME': temp})

                    folder = scratch / 'data/theme/light'
                    folder.mkdir(parents=True)
                    with zipfile.ZipFile(archive) as theme:
                        theme.extractall(folder)
                    if call('/api/admin/theme/set?theme=light').get('status') != 'success':
                        raise RuntimeError('theme switch failed')
                    with sync_playwright() as playwright:
                        browser = playwright.chromium.launch(headless=True)
                        try:
                            for width in (320, 390):
                                row = {'width': width, 'stages': []}
                                result['views'].append(row)
                                agent_process = start_agent()
                                time.sleep(6)
                                context = browser.new_context(viewport={'width': width, 'height': 900},
                                                              has_touch=True, is_mobile=True, service_workers='block')
                                context.route('**/*', lambda route: route.continue_() if
                                              route.request.url.startswith(base + '/') else route.abort())
                                page = context.new_page()
                                errors = []
                                page.on('pageerror', lambda error: errors.append(error))
                                try:
                                    response = page.goto(base + '/', wait_until='domcontentloaded', timeout=15000)
                                    row['http'] = response.status if response else None

                                    def control():
                                        return page.evaluate('''async id => await new Promise(resolve => {
                                          const ws = new WebSocket('ws://' + location.host + '/api/clients');
                                          const timer = setTimeout(() => { ws.close(); resolve(null); }, 3000);
                                          ws.onopen = () => ws.send('get');
                                          ws.onerror = () => { clearTimeout(timer); resolve(null); };
                                          ws.onmessage = e => { try { const m = JSON.parse(e.data);
                                            clearTimeout(timer); ws.close(); resolve({success:m.status==='success',
                                            online:Array.isArray(m.data?.online) && m.data.online.includes(id),
                                            hasReport:!!m.data?.data?.[id]});
                                          } catch { clearTimeout(timer); ws.close(); resolve(null); } };
                                        })''', node['uuid'])

                                    def await_control(expected_online):
                                        deadline = time.monotonic() + 35
                                        while time.monotonic() < deadline:
                                            value = control()
                                            if (value and value['success'] and value['online'] == expected_online
                                                    and (not expected_online or value['hasReport'])):
                                                return value
                                            page.wait_for_timeout(500)
                                        raise RuntimeError('independent presence control timed out')

                                    def await_theme(expected_online):
                                        deadline = time.monotonic() + 25
                                        observed = 'missing'
                                        while time.monotonic() < deadline:
                                            text = page.locator('body').inner_text(timeout=5000)
                                            if NODE_NAME in text:
                                                suffix = text.split(NODE_NAME, 1)[1].lstrip()
                                                observed = ('online' if suffix.startswith('在线') else
                                                            'offline' if suffix.startswith('离线') else 'other')
                                                if observed == ('online' if expected_online else 'offline'):
                                                    return {'matched': True, 'observed': observed}
                                            page.wait_for_timeout(500)
                                        return {'matched': False, 'observed': observed}

                                    for stage, expected_online in (('online', True), ('offline', False), ('reconnected', True)):
                                        if stage == 'offline':
                                            stop(agent_process)
                                            agent_process = None
                                        elif stage == 'reconnected':
                                            agent_process = start_agent()
                                        value = await_control(expected_online)
                                        row['stages'].append({'stage': stage, 'control': value,
                                                              'theme': await_theme(expected_online)})
                                    row['page_error_count'] = len(errors)
                                finally:
                                    context.close()
                                    stop(agent_process)
                                    agent_process = None
                        finally:
                            browser.close()
                    result['status'] = 'pass' if len(result['views']) == 2 and all(
                        row.get('http') == 200 and row.get('page_error_count') == 0 and
                        len(row['stages']) == 3 and all(stage['theme']['matched'] for stage in row['stages'])
                        for row in result['views']) else 'fail'
                finally:
                    stop(agent_process)
                    stop(panel_process)
    except Exception as error:
        result['error_type'] = type(error).__name__
    args.output.write_text(json.dumps(result, indent=2) + '\n')
    print(f"{result['status']}: {args.output}")
    return 0 if result['status'] == 'pass' else 1


if __name__ == '__main__':
    raise SystemExit(main())
