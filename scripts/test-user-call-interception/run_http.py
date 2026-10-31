"""Real two-process HTTP/SSE/WebSocket acceptance. Synthetic loopback only."""
import json, os, socket, struct, base64, secrets, subprocess, hashlib
from pathlib import Path
from urllib.request import Request, build_opener, ProxyHandler
from urllib.error import HTTPError
from client import ApiClient, NoRedirect
BASE=Path(__file__).resolve().parent
assert os.environ['SQL_DSN']=='postgres://interception_test@127.0.0.1:55883/interception_runtime?sslmode=disable', 'isolated fixture DSN required'
fixture=json.loads(Path(os.environ['UCI_FIXTURE_FILE']).read_text())
assert fixture.get('synthetic_only') is True and fixture['base_url']=='http://127.0.0.1:35881'
keys=fixture['keys']; opener=build_opener(ProxyHandler({}),NoRedirect()); events=[]
def emit(name,data=None):
 event={'test':name,'result':'passed',**(data or {})};events.append(event);print(json.dumps(event,ensure_ascii=False),flush=True)
def sql(query):
 return subprocess.check_output(['psql',os.environ['SQL_DSN'],'-XAt','-v','ON_ERROR_STOP=1','-c',query],text=True,env={'PATH':os.environ['PATH'],'HOME':str(Path(os.environ['UCI_FIXTURE_FILE']).parent/'home'),'PGPASSFILE':str(Path(os.environ['UCI_FIXTURE_FILE']).parent/'no-pgpass')}).strip()
def request(path,body=None,key='old',port=35881,method='POST',headers=None,status=200):
 raw=None if body is None else json.dumps(body).encode()
 h={'Content-Type':'application/json','Accept-Language':'en'}
 if key:h['Authorization']='Bearer '+keys[key]['key']
 h.update(headers or {})
 req=Request(f'http://127.0.0.1:{port}'+path,data=raw,headers=h,method=method)
 try:
  with opener.open(req,timeout=20) as r: code=r.status;result=r.read();rh=dict(r.headers)
 except HTTPError as e:code=e.code;result=e.read();rh=dict(e.headers)
 assert code==status, f'{method} {path.split("?")[0]} expected {status}, got {code}: {result[:350]!r}'
 return result,rh

def count():return json.loads(request('/counter',None,None,35886,'GET')[0])['count']
def snapshot():
 return sql("SELECT json_build_object('user',(SELECT row_to_json(x) FROM (SELECT quota,quota_paid,quota_bonus,quota_reward,used_quota,request_count,status FROM users WHERE id=4201)x),'tokens',(SELECT json_agg(x ORDER BY id) FROM (SELECT id,remain_quota,used_quota FROM tokens WHERE user_id=4201 AND deleted_at IS NULL)x),'channel',(SELECT used_quota FROM channels WHERE id=4401),'consume',(SELECT count(*) FROM logs WHERE type=2),'ledger',(SELECT count(*) FROM balance_ledger),'tasks',(SELECT count(*) FROM tasks),'mj',(SELECT count(*) FROM midjourneys))")
def frames(raw):
 data=[]
 for line in raw.decode().splitlines():
  if line.startswith('data: ') and line!='data: [DONE]':data.append(json.loads(line[6:]))
 return data

def open_ws():
 sock=socket.create_connection(('127.0.0.1',35881),timeout=15)
 key=base64.b64encode(secrets.token_bytes(16)).decode()
 req=(f'GET /v1/responses HTTP/1.1\r\nHost: 127.0.0.1:35881\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: {key}\r\nAuthorization: Bearer {keys["old"]["key"]}\r\n\r\n')
 sock.sendall(req.encode());h=b''
 while b'\r\n\r\n' not in h:h+=sock.recv(1)
 assert b'101 Switching Protocols' in h,h
 return sock

def ws_create(sock):
 raw=json.dumps({'type':'response.create','event_id':'synthetic-existing-ws','model':'gpt-4o-mini','input':'synthetic'}).encode();mask=secrets.token_bytes(4)
 header=bytes([0x81,0x80|126])+struct.pack('!H',len(raw)) if len(raw)>=126 else bytes([0x81,0x80|len(raw)])
 sock.sendall(header+mask+bytes(b^mask[i%4] for i,b in enumerate(raw)))
 def take(n):
  out=b''
  while len(out)<n:
   b=sock.recv(n-len(out));assert b,'websocket closed before an error event';out+=b
  return out
 h=take(2);n=h[1]&127
 if n==126:n=struct.unpack('!H',take(2))[0]
 if n==127:n=struct.unpack('!Q',take(8))[0]
 return json.loads(take(n))

raw,headers=request('/',None,None,method='GET')
assert b'<html' in raw.lower() and b'<script' in raw.lower(), 'full frontend-embedded binary required'
emit('full_frontend_embedded_binary_serves_html')

root=ApiClient(fixture['base_url'],fixture['accounts']['root'],emit);root.login()
user=ApiClient(fixture['base_url'],fixture['accounts']['target'],emit);user.login()
other=ApiClient(fixture['base_url'],fixture['accounts']['other'],emit);other.login()
configpath='/api/user/4201/call-interception'
config=root.get(configpath);assert not config['enabled'] and config['revision']==0
emit('fresh_default_disabled')
ws=open_ws();assert count()==0
reply='合成维护通知，不含真实客户文案。\nSynthetic maintenance notice.'
config=root.put(configpath,{'enabled':True,'reply':reply,'expected_revision':config['revision']});assert config['revision']==1
emit('admin_enable_persisted')
# Save on process A, observe immediately on independently running process B.
raw,_=request('/v1/chat/completions',{'model':'gpt-4o-mini','messages':[]},port=35882)
assert json.loads(raw)['choices'][0]['message']['content']==reply
emit('multi_node_save_a_read_b')
# Real owner profile updates cannot mass assign the policy.
user.put('/api/user/self',{'username':'uci_target','display_name':'Synthetic target','enabled':False,'reply':'injection','call_interception':{'enabled':False}})
assert root.get(configpath)['enabled'] is True
user.request('PUT',configpath,{'enabled':False,'reply':'attack','expected_revision':1},expected=403)
other.request('GET',configpath,expected=403)
emit('profile_mass_assignment_and_nonadmin_denied')
# Create a future key via the real user API, read it only from this synthetic DB.
user.post('/api/token/',{'name':'new-after-policy','unlimited_quota':True,'expired_time':-1,'group':''})
new=sql("SELECT json_build_object('id',id,'key','sk-' || key) FROM tokens WHERE user_id=4201 AND name='new-after-policy'")
keys['new']=json.loads(new)
for key in ('old','second','new','zero_token'):
 raw,_=request('/v1/chat/completions',{'model':'gpt-4o-mini','messages':[{'role':'user','content':'synthetic private prompt'}]},key=key)
 assert json.loads(raw)['choices'][0]['message']['content']==reply
assert sql('SELECT status FROM tokens WHERE id=4303')=='1'
emit('all_old_new_and_zero_budget_enabled_keys_intercepted')
for balance in (0,-31):
 sql(f'UPDATE users SET quota={balance},quota_paid={balance} WHERE id=4201')
 before=snapshot()
 for port in (35881,35882):
  raw,_=request('/v1/chat/completions',{'model':'gpt-4o-mini','messages':[]},port=port)
  assert json.loads(raw)['choices'][0]['message']['content']==reply
 assert snapshot()==before
emit('zero_negative_wallet_reply_and_zero_financial_effect')
financial_before=snapshot();upstream_before=count()
# Existing websocket established before enabling: each later event is rechecked.
response=ws_create(ws);ws.close()
assert response.get('type')=='error',response
assert response.get('error',{}).get('code')=='user_call_intercepted',response
emit('existing_responses_websocket_next_event_blocked')
for path,protocol in [('/v1/chat/completions','chat'),('/v1/responses','responses'),('/v1/messages','claude'),('/v1/completions','completions')]:
 for stream in (False,True):
  body={'model':'gpt-4o-mini','messages':[{'role':'user','content':'synthetic private prompt must not enter hit logs'}],'input':'synthetic','stream':stream}
  raw,headers=request(path,body)
  if stream:
   f=frames(raw);assert 'text/event-stream' in headers['Content-Type']
   if protocol=='responses':assert f[-1]['type']=='response.completed' and f[-1]['response']['usage']['total_tokens']==0
   elif protocol=='claude':assert f[-1]['type']=='message_stop' and f[-2]['delta']['stop_reason']=='end_turn'
   else:assert raw.rstrip().endswith(b'data: [DONE]')
  else:
   d=json.loads(raw);assert 'error' not in d or d['error'] is None
  assert reply.split('\n')[1] in raw.decode()
 emit(f'{protocol}_normal_sse_zero_usage')
for prefix in ('/v1/models','/v1beta/models'):
 for action in ('generateContent','streamGenerateContent'):
  raw,h=request(prefix+'/gpt-4o-mini:'+action+('?alt=sse' if action.startswith('stream') else ''),{'contents':[]})
  d=frames(raw)[0] if action.startswith('stream') else json.loads(raw)
  assert d['candidates'][0]['content']['parts'][0]['text']==reply and d['usageMetadata']['totalTokenCount']==0
raw,_=request('/v1beta/models/gpt-4o-mini:streamGenerateContent',{'contents':[]});assert isinstance(json.loads(raw),list)
emit('gemini_v1_v1beta_json_sse_and_stream_array')
for path in ('/pg/chat/completions','/api/user/playground/chat'):
 for stream in (False,True):
  raw,_=request(path,{'model':'gpt-4o-mini','messages':[],'stream':stream,'group':'default'},key=None,headers={'Authorization':'Bearer '+user.token})
  assert reply.split('\n')[1] in raw.decode()
emit('both_playground_entries_normal_sse_with_negative_balance')
for path in ('/v1/images/generations','/v1/images/edits','/v1/audio/speech','/v1/audio/transcriptions','/v1/embeddings','/v1/rerank','/v1/responses/compact','/v1/tasks/openai','/v1/video/generations','/v1/videos','/mj/submit/imagine','/fast/mj/submit/imagine'):
 raw,_=request(path,{'model':'gpt-4o-mini'},status=403);assert json.loads(raw)['error']['code']=='user_call_intercepted'
for path in ('/v1/responses','/v1/realtime','/v1/tasks/synthetic-intercept-video','/mj/task/synthetic-intercept-mj/fetch'):
 request(path,None,method='GET',status=403)
request('/mj/image/synthetic-intercept-mj',None,key=None,method='GET',status=403)
request('/v1/tasks/synthetic-intercept-video/artifacts/video/content?access='+fixture['artifact_access'],None,key=None,method='GET',status=403)
emit('binary_tasks_new_websockets_and_signed_public_artifacts_blocked')
# Check credential/authz failure wins over local text; no extra interception hit.
hit_before=int(sql('SELECT count(*) FROM user_call_interception_hits'))
for key in ('disabled','expired','exhausted'):
 request('/v1/chat/completions',{'model':'gpt-4o-mini'},key=key,status=401)
for key in ('limited','ip_limited','bad_group'):
 request('/v1/chat/completions',{'model':'gpt-4o-mini'},key=key,status=403)
request('/v1/chat/completions',{'model':'gpt-4o-mini'},key=None,status=401)
request('/v1/chat/completions',{'model':'gpt-4o-mini'},headers={'Authorization':'Bearer sk-invalid-synthetic'},status=401)
raw,_=request('/v1/chat/completions',{'model':'gpt-4o-mini','user_id':4202,'user':'4202','intercept':False},headers={'New-Api-User':'4202','X-User-ID':'4202','X-Relay-User-Notice':'false'})
assert json.loads(raw)['choices'][0]['message']['content']==reply
sql('UPDATE users SET status=2 WHERE id=4201')
request('/v1/chat/completions',{'model':'gpt-4o-mini'},status=403)
sql('UPDATE users SET status=1 WHERE id=4201')
assert int(sql('SELECT count(*) FROM user_call_interception_hits'))==hit_before+1
emit('invalid_expired_disabled_model_ip_group_bans_and_forged_ids')
assert count()==upstream_before==0
financial_after=snapshot()
assert financial_after==financial_before
(Path(os.environ['UCI_EVIDENCE_DIR'])/'financial-snapshots.json').write_text(json.dumps({'before':json.loads(financial_before),'after':json.loads(financial_after),'upstream_before':upstream_before,'upstream_after':count()},indent=2))
emit('all_interception_cases_upstream_counter_zero_financial_snapshot_identical',{'snapshot_sha256':hashlib.sha256(financial_after.encode()).hexdigest(),'upstream_calls':0})
# Safe metadata only, with source attribution for public/capability resources.
hits=root.get(configpath+'/hits?page_size=100');assert hits['total']>=30
assert set(hits['items'][0])=={'id','time','user_id','actor_user_id','identity_kind','token_id','request_id','interface','protocol','status','revision'}
alltext=json.dumps(hits,ensure_ascii=False)
for forbidden in (reply,'synthetic private prompt',keys['old']['key'],fixture['artifact_access']):assert forbidden not in alltext
assert any(h['identity_kind']=='public_artifact' and h['actor_user_id']==0 for h in hits['items'])
assert any(h['identity_kind']=='artifact_capability' and h['actor_user_id']==0 for h in hits['items'])
selected=hits['items'][0];filtered=root.get(configpath+'/hits?request_id='+selected['request_id']);assert filtered['total']==1
emit('independent_hit_logs_query_safe_metadata_and_identity_attribution')
# Immediate edit, stale revision conflict, and persistent read on other process.
config=root.put(configpath,{'enabled':True,'reply':'Updated synthetic reply\n已更新','expected_revision':config['revision']})
root.request('PUT',configpath,{'enabled':False,'reply':'stale','expected_revision':1},expected=409)
raw,_=request('/v1/chat/completions',{'model':'gpt-4o-mini'},port=35882);assert json.loads(raw)['choices'][0]['message']['content']==config['reply']
emit('edit_immediate_cross_node_and_stale_write_conflict')
# Fail closed if configuration read or mandatory hit recording becomes unavailable.
for table in ('user_call_interception_hits','user_call_interceptions'):
 sql(f'ALTER TABLE {table} RENAME TO {table}_unavailable')
 try: request('/v1/chat/completions',{'model':'gpt-4o-mini'},status=503)
 finally:sql(f'ALTER TABLE {table}_unavailable RENAME TO {table}')
assert count()==0
emit('configuration_read_and_hit_persistence_failure_fail_closed')
audits=root.get(configpath+'/audits');assert audits['total']==2
assert all('reply' not in a for a in audits['items'])
emit('configuration_audits_atomic_and_text_free')
# Other authenticated user cannot impersonate intercepted target and still works.
raw,_=request('/v1/chat/completions',{'model':'gpt-4o-mini','messages':[{'role':'user','content':'synthetic'}],'user_id':4201},key='other',headers={'New-Api-User':'4201'})
assert json.loads(raw)['choices'][0]['message']['content']=='normal upstream fixture'
assert count()==1
emit('other_user_unaffected_even_with_forged_target_id')
config=root.put(configpath,{'enabled':False,'reply':config['reply'],'expected_revision':config['revision']})
sql('UPDATE users SET quota=1000000,quota_paid=1000000 WHERE id=4201')
raw,_=request('/v1/chat/completions',{'model':'gpt-4o-mini','messages':[{'role':'user','content':'synthetic'}]},port=35882)
assert json.loads(raw)['choices'][0]['message']['content']=='normal upstream fixture' and count()==2
emit('disable_restores_normal_processing_cross_node')
assert root.get(configpath+'/audits')['total']==3
(Path(os.environ['UCI_EVIDENCE_DIR'])/'http-results.json').write_text(json.dumps({'tests':events,'count':len(events),'synthetic_only':True,'upstream_calls_during_interception':0,'normal_fixture_calls_after_off':2},ensure_ascii=False,indent=2))
print('ALL SYNTHETIC HTTP/SSE/WEBSOCKET ACCEPTANCE PASSED',flush=True)
