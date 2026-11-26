#!/usr/bin/env python3
"""Linux/amd64, loopback-only drill; see docs/EXEC-HISTORY-DESIGN.md. Uses no Agent."""
import argparse
from contextlib import closing, contextmanager
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import socket
import sqlite3
import subprocess
import tempfile
import time
from urllib.error import URLError
from urllib.request import build_opener, ProxyHandler, Request


OLD_VERSION = 'v1.8.2'
OLD_HASH = 'd58ec32'
OLD_SHA256 = 'e26d95ea0c4003a4755cab123e903809ef518c8bfeea425a7abac10ae8fce1d8'


def check(ok, message):
    if not ok:
        raise RuntimeError(message)


def drill(old, candidate, candidate_hash, root):
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    address = f'127.0.0.1:{port}'
    http = build_opener(ProxyHandler({}))

    def call(path, data=None):
        req = Request(f'http://{address}{path}', headers={'Content-Type': 'application/json'},
                      data=None if data is None else json.dumps(data).encode())
        with http.open(req, timeout=3) as res:
            value = json.load(res)
        check(value.get('status') == 'success', f'{path}: not successful')
        return value.get('data')

    @contextmanager
    def boot(binary, expected, install=False):
        with (root / 'panel.log').open('ab') as log:
            panel = subprocess.Popen([binary, 'server', '--listen', address], cwd=root,
                                     env={'HOME': str(root), 'PATH': os.defpath, 'TZ': 'UTC'},
                                     stdout=log, stderr=subprocess.STDOUT)
            try:
                def wait(path):
                    deadline = time.monotonic() + 30
                    while time.monotonic() < deadline:
                        check(panel.poll() is None, f'{expected}: panel exited during startup')
                        try:
                            return call(path)
                        except (URLError, OSError, ValueError):
                            time.sleep(.1)
                    raise RuntimeError(f'{expected}: timeout waiting for {path}')

                if install:
                    wait('/api/install/status')
                    call('/api/install/complete', {'username': 'drill', 'password': secrets.token_urlsafe(32),
                         'sitename': 'Rollback drill', 'metric_dsn': './data/metrics.db'})
                version = wait('/api/version')
                check(version['hash'] == expected, 'unexpected panel build')
                if binary == old:
                    check(version['version'] == OLD_VERSION, 'unexpected release')
                with closing(sqlite3.connect(root / 'data/komari.db')) as db:
                    db.execute('pragma foreign_keys=on')
                    yield db
                    check(db.execute('pragma quick_check').fetchall() == [('ok',)], 'quick_check failed')
            finally:
                if panel.poll() is None:
                    panel.terminate()
                try:
                    code = panel.wait(timeout=20)
                except subprocess.TimeoutExpired:
                    panel.kill()
                    panel.wait()
                    raise RuntimeError(f'{expected}: unclean shutdown')
            check(code == 0, f'{expected}: unexpected exit {code}')

    def seed(db, key):
        with db:
            db.execute('insert into tasks(task_id,clients,command) values(?,?,?)',
                       (key, '["fixture-node"]', 'command marker ' + key))
            db.execute('insert into task_results(task_id,client,result,exit_code,created_at,finished_at) '
                       'values(?,?,?,0,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)',
                       (key, 'fixture-node', 'output marker ' + key))

    def snapshot(db, keys):
        rows = db.execute('select t.task_id, t.history_order, r.history_identity, t.command, r.result '
                          'from tasks t join task_results r on r.task_id=t.task_id order by t.history_order').fetchall()
        check(db.execute('select count(*) from tasks').fetchone()[0] == len(keys) and
              db.execute('select count(*) from task_results').fetchone()[0] == len(keys), 'row counts changed')
        check([r[0] for r in rows] == keys, 'task order or membership changed')
        orders, ids = set(), set()
        for key, order, identity, command, output in rows:
            check(isinstance(order, int) and order > 0 and order not in orders, 'invalid task order')
            check(re.fullmatch('[0-9a-f]{32}', identity or '') and identity not in ids, 'invalid result identity')
            check((command, output) == ('command marker ' + key, 'output marker ' + key), 'payload changed')
            orders.add(order)
            ids.add(identity)
        return rows

    keys = ['before-upgrade', 'during-rollback']
    with boot(old, OLD_HASH, install=True) as db:
        check('history_order' not in [r[1] for r in db.execute('pragma table_info(tasks)')], 'not the old schema')
        with db:
            db.execute('insert into clients(uuid,token) values(?,?)', ('fixture-node', secrets.token_hex(32)))
        seed(db, keys[0])
    with boot(candidate, candidate_hash) as db:
        before = snapshot(db, keys[:1])
    with boot(old, OLD_HASH) as db:
        check(snapshot(db, keys[:1]) == before, 'rollback changed upgraded rows')
        seed(db, keys[1])
        order, identity = db.execute('select t.history_order, r.history_identity from tasks t join task_results r '
                                     'on t.task_id=r.task_id where t.task_id=?', (keys[1],)).fetchone()
        check(order is None and re.fullmatch('[0-9a-f]{32}', identity or ''), 'legacy insert/trigger mismatch')
    with boot(candidate, candidate_hash) as db:
        after = snapshot(db, keys)
        check(after[:1] == before and after[1][2] == identity, 'forward migration changed an identity or old row')
    with boot(candidate, candidate_hash) as db:
        check(snapshot(db, keys) == after, 'second candidate startup was not idempotent')
    print(f'PASS: {OLD_VERSION} -> candidate -> {OLD_VERSION} + legacy SQL insert -> candidate -> candidate; '
          'orders, identities, contents, quick_check and clean stops verified')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('old', type=Path)
    parser.add_argument('candidate', type=Path)
    parser.add_argument('candidate_hash', help='VersionHash compiled into the candidate')
    args = parser.parse_args()
    check({name for _, name in socket.if_nameindex()} == {'lo'}, 'run in a new loopback-only network namespace')
    check(re.fullmatch('[0-9a-f]{7,40}', args.candidate_hash) and args.candidate_hash != OLD_HASH,
          'expected a distinct candidate commit')
    with args.old.open('rb') as f:
        check(hashlib.file_digest(f, 'sha256').hexdigest() == OLD_SHA256, 'not the published Linux amd64 panel')
    os.umask(0o077)
    with tempfile.TemporaryDirectory(prefix='exec-history-drill-') as tmp:
        drill(args.old.resolve(strict=True), args.candidate.resolve(strict=True), args.candidate_hash, Path(tmp))
