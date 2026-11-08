import json, re, sys, zipfile
from datetime import datetime, timezone, timedelta
from pathlib import Path, PurePosixPath
from playwright.sync_api import sync_playwright
z=zipfile.ZipFile(sys.argv[1])
contract=json.loads(Path(__file__).with_name('ping-role-contract.json').read_text())
manifest=json.loads(z.read('komari-theme.json'))
assert manifest['short']=='emerald-insights' and manifest['version']=='2.0.6'
reqs=[]
def route(r):
  path=r.request.url.split('https://fixture.test',1)[1].split('?',1)[0]
  reqs.append(path)
  if path=='/api/rpc2':
    req=json.loads(r.request.post_data); method=req['method']; params=req.get('params') or {}; hours=params.get('hours',1); now=datetime.now(timezone.utc)
    def pts(value):
      return [{'time':(now-timedelta(hours=hours)+timedelta(seconds=i*hours*3600/30)).isoformat(),'value':value} for i in range(31)]
    if method=='rpc.ping': result='pong'
    elif method=='public:getPingMetricStats':
      result={'stats':[dict(s, name=contract['legacy_task']['name'] if s['task_id']=='28' else contract['pinned_task']['name'], interval=60, loss=0, avg=s['value'], total=31) for s in contract['series']]}
    elif method=='public:queryMetrics':
      k=params['metric_keys'][0]
      result={'series':[{'metric_key':k, 'tags':{a:b for a,b in s.items() if a!='value'}, 'points':pts(0 if k=='ping.loss' else s['value'])} for s in contract['series']]}
    elif method=='common:getPublicInfo': result={'sitename':'Fixture','ping_record_preserve_time':2160,'record_enabled':True,'theme_settings':{'showPingReference':enabled},'private_site':False}
    elif method=='common:getNodes': result={'fixture':{'uuid':'fixture','name':'Fixture','region':'HK','group':'','tags':'','weight':0,'billing_cycle':1,'currency':'CNY','price':0,'mem_total':1024,'disk_total':1024,'swap_total':0,'os':'Linux','arch':'amd64','cpu_cores':1,'cpu_name':'Fixture','virtualization':'kvm','hidden':False,'traffic_limit':0,'traffic_limit_type':'sum','expired_at':'2030-01-01T00:00:00Z','created_at':'2026-01-01T00:00:00Z','updated_at':'2026-01-01T00:00:00Z'}}
    elif method=='common:getNodesLatestStatus': result={'fixture':{'client':'fixture','time':now.isoformat(),'cpu':1,'ram':1,'ram_total':1024,'swap':0,'swap_total':0,'disk':1,'disk_total':1024,'net_in':0,'net_out':0,'net_total_up':0,'net_total_down':0,'online':True,'uptime':100,'load':0,'load5':0,'load15':0,'process':1,'connections':1,'connections_udp':0}}
    elif method in ('common:getRecords','common:getNodeRecentStatus'): result={'records':[],'tasks':[]}
    elif method in ('rpc.getVersion','common:getBackendVersion'): result={'version':'v1.7.29','hash':'94e1973'}
    else:
      r.fulfill(status=200,content_type='application/json',body=json.dumps({'jsonrpc':'2.0','id':req['id'],'error':{'code':-32601,'message':'Method not found'}}));return
    r.fulfill(status=200,content_type='application/json',body=json.dumps({'jsonrpc':'2.0','id':req['id'],'result':result}));return
  if path in ('/api/public','/api/me','/api/version'):
    data={'ping_record_preserve_time':2160,'record_preserve_time':2160,'record_enabled':True,'sitename':'Fixture','theme_settings':{'rpcTransportMode':'http','showPingReference':enabled}} if path=='/api/public' else {'logged_in':True} if path=='/api/me' else {'version':'v1.7.29','hash':'94e1973'}
    r.fulfill(status=200,content_type='application/json',body=json.dumps(data if path=='/api/me' else {'status':'success','data':data}));return
  p='dist'+path
  if p=='dist/' or not PurePosixPath(path).suffix: p='dist/index.html'
  if p in z.namelist():
    ct='application/javascript' if p.endswith('.js') else 'text/css' if p.endswith('.css') else 'text/html' if p.endswith('.html') else 'application/octet-stream'
    r.fulfill(status=200,content_type=ct,body=z.read(p));return
  r.fulfill(status=404,body='')
with sync_playwright() as p:
  b=p.chromium.launch(headless=True,args=['--no-sandbox'])
  for width, enabled in ((1440,False),(390,False),(1440,True),(390,True)):
    page=b.new_page(viewport={'width':width,'height':950});errors=[];requests=[]
    page.on('pageerror',lambda e:errors.append(str(e)))
    page.add_init_script("window.WebSocket = class { static OPEN=1; static CLOSED=3; constructor(){ this.readyState=3; setTimeout(()=>this.onerror?.(new Event('error')),0) } close(){} }")
    page.route('https://**/*',lambda r: route(r) if r.request.url.startswith('https://fixture.test/') else r.abort())
    page.goto('https://fixture.test/instance/fixture',wait_until='domcontentloaded')
    page.get_by_text('Education IPv6 (IPV6 / ICMP / 主目标)',exact=True).wait_for(timeout=20000)
    page.get_by_text('Education IPv6 (IPV6 / TCP / 主目标)',exact=True).wait_for()
    ref=page.get_by_text('Education IPv6 (IPV6 / ICMP / 参考点)',exact=True)
    assert ref.count()==(1 if enabled else 0), (width,enabled,ref.count())
    assert '角色未标记' not in page.locator('body').inner_text()
    assert page.locator('canvas').count()>0
    if enabled: ref.click()
    for period in ('7 天','30 天'):
      page.get_by_role('tab',name=period,exact=True).last.click()
      page.get_by_role('button',name='平滑峰值').click()
      assert page.locator('canvas').count()>0
      page.get_by_role('button',name='平滑峰值').click()
    assert not errors,errors
    assert any(re.search(r'PingChart-[^/]+\.js$',x) for x in reqs)
    print(f'actual zip full instance width={width} reference={enabled}: roles, selection, chart, 7/30-day smoothing PASS')
    page.close()
  b.close()
