import hashlib, json, os, pathlib, secrets, socket, subprocess, tempfile, time, zipfile
from http.cookiejar import CookieJar
from urllib.request import Request, build_opener, HTTPCookieProcessor, ProxyHandler
from urllib.error import URLError
from playwright.sync_api import sync_playwright
import argparse
parser=argparse.ArgumentParser(description="Preliminary isolated 64-theme smoke scan; not a full compatibility acceptance")
parser.add_argument("--panel-binary",type=pathlib.Path,required=True)
parser.add_argument("--archives",type=pathlib.Path,required=True)
parser.add_argument("--output",type=pathlib.Path,required=True)
args=parser.parse_args()
root=pathlib.Path(__file__).resolve().parents[2]; binary=str(args.panel_binary.resolve()); lock=json.loads((root/'web/api/admin/testdata/komari-market-20261108.lock.json').read_text())
output=args.output.resolve(); results=[]
def write_report(status):
 output.write_text(json.dumps({'status':status,'panel_sha256':hashlib.sha256(pathlib.Path(binary).read_bytes()).hexdigest(),'results':results},ensure_ascii=False,indent=2))
with socket.socket() as sock:
 sock.bind(('127.0.0.1',0)); port=sock.getsockname()[1]
base=f'http://127.0.0.1:{port}'; opener=build_opener(ProxyHandler({}),HTTPCookieProcessor(CookieJar()))
def call(path,data=None):
 req=Request(base+path,data=None if data is None else json.dumps(data).encode(),headers={'Content-Type':'application/json'})
 with opener.open(req,timeout=15) as r: return json.load(r)
with tempfile.TemporaryDirectory(prefix='nekomari-theme-v181-') as tmp:
 scratch=pathlib.Path(tmp); log=(scratch/'panel.log').open('wb'); proc=subprocess.Popen([binary,'-d',str(scratch/'panel.db'),'server','--listen',f'127.0.0.1:{port}'],cwd=scratch,stdout=log,stderr=subprocess.STDOUT,env={**os.environ,'HOME':tmp})
 try:
  for _ in range(150):
   try: call('/api/install/status'); break
   except (URLError,TimeoutError): time.sleep(.1)
  else: raise RuntimeError('panel failed to start')
  username='theme-fixture'; password='Theme-fixture1-'+secrets.token_urlsafe(20)
  assert call('/api/install/complete',{'username':username,'password':password,'sitename':'Theme fixture','metric_dsn':str(scratch/'metrics.db')})['status']=='success'
  for _ in range(100):
   try: call('/api/version'); break
   except (URLError,TimeoutError): time.sleep(.1)
  assert call('/api/login',{'username':username,'password':password})['status']=='success'
  theme_root=scratch/'data/theme'; theme_root.mkdir(parents=True,exist_ok=True)
  with sync_playwright() as pw:
   browser=pw.chromium.launch(headless=True)
   try:
    for item in lock['items']:
     name=item['short']; archive=args.archives/(name+'.zip'); raw=archive.read_bytes(); row={'short':name,'version':item.get('version'),'sha256':hashlib.sha256(raw).hexdigest(),'views':[]}
     try:
      if row['sha256']!=item['archive_sha256']: raise RuntimeError('hash mismatch')
      folder=theme_root/name; folder.mkdir(exist_ok=True)
      with zipfile.ZipFile(archive) as z: z.extractall(folder)
      response=call('/api/admin/theme/set?theme='+name)
      if response.get('status')!='success': raise RuntimeError('theme switch refused')
      for width in [1280,390]:
       context=browser.new_context(viewport={'width':width,'height':900},service_workers='block'); context.route('**/*', lambda route: route.continue_() if route.request.url.startswith(base + '/') else route.abort()); page=context.new_page(); errors=[]; console=[]; page.on('pageerror',lambda e: errors.append(str(e).replace(base, 'http://localhost:<port>')[:300])); page.on('console',lambda m: console.append(m.text.replace(base, 'http://localhost:<port>')[:300]) if m.type=='error' else None)
       try:
        response=page.goto(base+'/',wait_until='domcontentloaded',timeout=15000); page.wait_for_timeout(1200)
        row['views'].append({'width':width,'http':response.status if response else None,'body_text_length':len(page.locator('body').inner_text(timeout=3000).strip()),'page_errors':errors[:10],'console_errors':console[:10]})
       except Exception as exc: row['views'].append({'width':width,'error':str(exc)[:300],'page_errors':errors[:10],'console_errors':console[:10]})
       finally: context.close()
     except Exception as exc: row['error']=str(exc)[:300]
     results.append(row); write_report('running')
     print(name, flush=True)
   finally: browser.close()
 finally:
  proc.terminate()
  try: proc.wait(timeout=10)
  except subprocess.TimeoutExpired: proc.kill(); proc.wait()
  log.close(); write_report('completed')
