#!/usr/bin/env python3
"""Check the locked btop-terminal theme against a real Agent in a disposable Panel.

Requires Playwright Chromium. Run in a network-isolated environment: browser
requests are restricted to the Panel, but the Agent may perform its own lookups.
Exit 0 only if both mobile views agree with the Panel's online status; exit 1
on a mismatch or incomplete check. Reports contain no host metrics or tokens.
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
    if process is None:
        return
    process.terminate()
    try:
        process.wait(timeout=10)
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
    item = next(i for i in items if i['short'] == 'btop-terminal')
    archive_hash = digest(archive)
    if archive_hash != item['archive_sha256']:
        parser.error('archive does not match the locked btop-terminal ZIP')
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    base = f'http://127.0.0.1:{port}'
    opener = build_opener(ProxyHandler({}), HTTPCookieProcessor(CookieJar()))

    def call(path, data=None):
        request = Request(base + path, data=None if data is None else json.dumps(data).encode(),
                          headers={'Content-Type': 'application/json'})
        with opener.open(request, timeout=15) as response:
            return json.load(response)

    result = {'panel_sha256': digest(panel), 'agent_sha256': digest(agent),
              'archive_sha256': archive_hash, 'views': [], 'status': 'incomplete'}
    process = agent_process = None
    try:
        with tempfile.TemporaryDirectory(prefix='nekomari-btop-check-') as temp:
            scratch = Path(temp)
            with (scratch / 'panel.log').open('wb') as panel_log, (scratch / 'agent.log').open('wb') as agent_log:
                try:
                    process = subprocess.Popen([str(panel), '-d', str(scratch / 'panel.db'), 'server',
                                                '--listen', f'127.0.0.1:{port}'], cwd=scratch,
                                               stdout=panel_log, stderr=subprocess.STDOUT,
                                               env={**os.environ, 'HOME': temp})
                    for _ in range(150):
                        try:
                            call('/api/install/status')
                            break
                        except (URLError, TimeoutError):
                            time.sleep(.1)
                    else:
                        raise RuntimeError('Panel did not start')
                    password = secrets.token_urlsafe(32)
                    installed = call('/api/install/complete', {'username': 'theme-fixture', 'password': password,
                                     'sitename': 'Theme fixture', 'metric_dsn': str(scratch / 'metrics.db')})
                    if installed.get('status') != 'success':
                        raise RuntimeError('Panel setup failed')
                    for _ in range(100):
                        try:
                            call('/api/version')
                            break
                        except (URLError, TimeoutError):
                            time.sleep(.1)
                    if call('/api/login', {'username': 'theme-fixture', 'password': password}).get('status') != 'success':
                        raise RuntimeError('Panel login failed')
                    node = call('/api/admin/client/add', {'name': NODE_NAME})
                    token_file = scratch / 'agent.token'
                    token_file.write_text(node['token'])
                    token_file.chmod(0o600)
                    agent_process = subprocess.Popen([str(agent), '--endpoint', base, '--token-file', str(token_file),
                                                     '--interval', '1', '--disable-auto-update', '--disable-web-ssh'],
                                                    cwd=scratch, stdout=agent_log, stderr=subprocess.STDOUT,
                                                    env={**os.environ, 'HOME': temp})
                    folder = scratch / 'data/theme/btop-terminal'
                    folder.mkdir(parents=True)
                    with zipfile.ZipFile(archive) as theme:
                        theme.extractall(folder)
                    if call('/api/admin/theme/set?theme=btop-terminal').get('status') != 'success':
                        raise RuntimeError('theme switch failed')
                    with sync_playwright() as playwright:
                        browser = playwright.chromium.launch(headless=True)
                        try:
                            for width in (320, 390):
                                context = browser.new_context(viewport={'width': width, 'height': 900},
                                                              service_workers='block', has_touch=True, is_mobile=True)
                                context.route('**/*', lambda route: route.continue_() if
                                              route.request.url.startswith(base + '/') else route.abort())
                                page = context.new_page()
                                errors = []
                                page.on('pageerror', lambda error: errors.append(str(error)[:300]))
                                try:
                                    response = page.goto(base + '/', wait_until='domcontentloaded', timeout=15000)
                                    page.wait_for_timeout(5000)
                                    control = page.evaluate('''async (uuid) => await new Promise(resolve => {
                                      const ws = new WebSocket((location.protocol === 'https:' ? 'wss:' : 'ws:') + '//' + location.host + '/api/clients');
                                      const timer = setTimeout(() => { ws.close(); resolve({timeout:true}); }, 4000);
                                      ws.onopen = () => ws.send('get');
                                      ws.onerror = () => { clearTimeout(timer); resolve({error:true}); };
                                      ws.onmessage = e => { try { const m=JSON.parse(e.data); clearTimeout(timer); ws.close();
                                        resolve({success:m.status==='success', online:Array.isArray(m.data?.online) && m.data.online.includes(uuid),
                                          hasReport:!!m.data?.data?.[uuid]}); } catch { clearTimeout(timer); ws.close(); resolve({invalid:true}); } };
                                    })''', node['uuid'])
                                    text = page.locator('body').inner_text(timeout=5000)
                                    result['views'].append({'width': width, 'http': response.status if response else None,
                                                            'node_visible': NODE_NAME in text,
                                                            'theme_online': 'ONLINE: ● 1' in text,
                                                            'control': control, 'page_error_count': len(errors)})
                                finally:
                                    context.close()
                        finally:
                            browser.close()
                    result['status'] = 'pass' if all(
                        v['http'] == 200 and v['node_visible'] and v['theme_online'] and not v['page_error_count']
                        and v['control'] == {'success': True, 'online': True, 'hasReport': True}
                        for v in result['views']) and len(result['views']) == 2 else 'fail'
                finally:
                    stop(agent_process)
                    stop(process)
    except Exception as error:
        result['error_type'] = type(error).__name__
    args.output.write_text(json.dumps(result, indent=2) + '\n')
    print(f"{result['status']}: {args.output}")
    return 0 if result['status'] == 'pass' else 1


if __name__ == '__main__':
    raise SystemExit(main())
