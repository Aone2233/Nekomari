"""Click the built-in chart's wide CSV button on a disposable panel with real rollups."""
import argparse
import codecs
from contextlib import closing
import csv
import hashlib
import io
import json
import os
from pathlib import Path
import secrets
import socket
import sqlite3
import subprocess
import tempfile
import time
from urllib.error import URLError
from urllib.request import ProxyHandler, build_opener

from playwright.sync_api import expect, sync_playwright


def seed(path, entity):
    # Closed minutes, inside the 1 h window. The second minute has no CPU sample.
    first = int(time.time() // 60 - 12) * 60000
    with closing(sqlite3.connect(path)) as db, db:
        db.execute('INSERT OR IGNORE INTO metric_resolutions(resolution_milli) VALUES (60000)')
        resolution, = db.execute('SELECT id FROM metric_resolutions WHERE resolution_milli=60000').fetchone()
        digest = hashlib.sha256(b'{}').hexdigest()
        db.execute('INSERT OR IGNORE INTO metric_label_sets(labels_hash, labels) VALUES (?, ?)', (digest, '{}'))
        label, = db.execute('SELECT id FROM metric_label_sets WHERE labels_hash=?', (digest,)).fetchone()
        for name, values in [('cpu.usage', [12.5]), ('memory.used', [4194304, 8388608])]:
            series = db.execute('INSERT INTO metric_series(metric_name,entity_id,tags_hash,tags) VALUES (?,?,?,?)',
                                (name, entity, digest, '{}')).lastrowid
            for minute, value in enumerate(values):
                stamp = first + 60000 * minute
                db.execute('''INSERT INTO metric_rollups(series_id,resolution_id,label_id,bucket_milli,count,sum,
                    sum_sq,min_val,max_val,first_val,first_ts_milli,last_val,last_ts_milli,created_at_milli)
                    VALUES (?,?,?,?,1,?,?,?,?,?,?,?,?,?)''', (series, resolution, label, stamp, value,
                           value**2, value, value, value, stamp, value, stamp, stamp))
    return first


def run(binary, root):
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        address = '127.0.0.1:%d' % sock.getsockname()[1]
    origin = 'http://' + address
    http = build_opener(ProxyHandler({}))
    env = {'HOME': str(root), 'PATH': os.defpath, 'TZ': 'UTC'}
    login = {'username': 'csv-fixture', 'password': secrets.token_urlsafe(32)}

    def wait(path):
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline and panel.poll() is None:
            try:
                with http.open(origin + path, timeout=2) as response:
                    if json.load(response)['status'] == 'success':
                        return
            except (URLError, TimeoutError, ValueError):
                pass
            time.sleep(.1)
        raise RuntimeError(f'scratch panel not ready: {path}')

    def stop():
        panel.terminate()
        try:
            panel.wait(timeout=20)
        except subprocess.TimeoutExpired:
            panel.kill()
            panel.wait()
            raise
        assert panel.returncode == 0, f'unclean panel exit: {panel.returncode}'

    with (root / 'panel.log').open('wb') as log, sync_playwright() as pw:
        def start():
            return subprocess.Popen([binary, 'server', '--listen', address], cwd=root, env=env,
                                    stdout=log, stderr=subprocess.STDOUT)

        panel = start()
        try:
            wait('/api/install/status')
            request = pw.request.new_context(base_url=origin)
            try:
                assert request.post('/api/install/complete', data={**login, 'sitename': 'Chart CSV',
                    'metric_dsn': './data/metrics.db'}).json()['status'] == 'success'
                wait('/api/version')
                assert request.get('/api/public').json()['data']['theme'] == 'default'
                assert request.post('/api/login', data=login).json()['status'] == 'success'
                assert request.post('/api/admin/settings/', data={'eula_accepted': True, 'geo_ip_enabled': False,
                    'login_notification': False, 'notification_enabled': False}).json()['status'] == 'success'
                entity = request.post('/api/admin/client/add', data={'name': 'CSV fixture'}).json()['uuid']
            finally:
                request.dispose()
        finally:
            stop()
        first = seed(root / 'data/metrics.db', entity)
        panel = start()
        try:
            wait('/api/version')
            browser = pw.chromium.launch(headless=True)
            try:
                ctx = browser.new_context(base_url=origin, locale='zh-CN', timezone_id='UTC', service_workers='block')
                try:
                    assert ctx.request.post('/api/login', data=login).json()['status'] == 'success'
                    version = ctx.request.get('/api/version').json()['data']
                    ctx.add_init_script("localStorage.setItem('language','zh-CN')")
                    page = ctx.new_page()
                    page.goto('/instance/' + entity, wait_until='domcontentloaded')
                    page.get_by_role('radio', name='1 小时', exact=True).click()
                    button = page.get_by_role('button', name='导出 CSV', exact=True)
                    expect(button).to_be_enabled()
                    with page.expect_download() as event:
                        button.click()
                    download = event.value
                    assert download.suggested_filename.startswith(f'nekomari-{entity}-1h-')
                    assert download.suggested_filename.endswith('.csv')
                    data = Path(download.path()).read_bytes()
                    assert data.startswith(codecs.BOM_UTF8) and b'\r\n' in data
                    reader = csv.DictReader(io.StringIO(data.decode('utf-8-sig'), newline=''))
                    assert reader.fieldnames[0] == 'time' and {'cpu.usage', 'memory.used'} <= set(reader.fieldnames)
                    rows = {r['time']: r for r in reader}
                    def at(ms):
                        return rows[time.strftime('%Y-%m-%dT%H:%M:%S.000Z', time.gmtime(ms / 1000))]
                    assert float(at(first)['cpu.usage']) == 12.5 and int(at(first)['memory.used']) == 4194304
                    assert at(first + 60000)['cpu.usage'] == '' and int(at(first + 60000)['memory.used']) == 8388608
                    assert page.url == origin + '/instance/' + entity
                    print('PASS:', version, 'Chinese chart download; expected CPU and memory samples and blank gap')
                finally:
                    ctx.close()
            finally:
                browser.close()
        finally:
            stop()


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--panel-binary', type=Path, required=True)
    args = parser.parse_args()
    if {name for _, name in socket.if_nameindex()} != {'lo'}:
        parser.error('run in a loopback-only network namespace')
    os.umask(0o077)
    with tempfile.TemporaryDirectory(prefix='metric-csv-panel-') as tmp:
        run(args.panel_binary.resolve(strict=True), Path(tmp))
