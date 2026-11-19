#!/usr/bin/env python3
"""Offline im.13 -> im.18 -> verification-only im.13 -> im.18 rehearsal; new local stores only."""
import argparse, contextlib, hashlib, json, os, re, secrets, socket, subprocess as sp, tempfile, threading, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib import request, error

if not __debug__:
    raise SystemExit("Do not use python -O: acceptance assertions are required.")
parser = argparse.ArgumentParser(description=__doc__)
for arg in ("im13", "im18", "rollback", "pg-bin", "redis-bin", "output"):
    parser.add_argument("--"+arg, type=lambda p: Path(p).absolute(), required=True)
parser.add_argument("--padding-rows", type=int, default=256, help="Synthetic short report rows before widening (0..100000)")
parser.add_argument("--wait-binaries", type=int, default=0, help="Wait this many seconds for the rollback build")
a = parser.parse_args()
if not 0 <= a.padding_rows <= 100000:parser.error("padding rows must be in [0,100000]")
if any(os.environ.get(k) for k in ("SQL_DSN", "LOG_SQL_DSN","REDIS_CONN_STRING")):
    raise SystemExit("Unset existing datastore DSNs. This harness only provisions fresh loopback fixtures.")
os.umask(0o077)
a.output.mkdir(parents=True, exist_ok=True)
run = Path(tempfile.mkdtemp(prefix="evidence-", dir=a.output)); tmp = tempfile.TemporaryDirectory(prefix="im18-rollback-fixture-"); work=Path(tmp.name)
env={"HOME":str(work),"PATH":os.environ["PATH"],"LC_ALL":"C","PGHOST":"127.0.0.1","PGUSER":"rehearsal","PGPASSWORD":secrets.token_hex(24),"PGCONNECT_TIMEOUT":"3"}
rpw=secrets.token_hex(24); loginpw="LocalOnly#"+secrets.token_hex(16)
session_vars={"SESSION_SECRET":secrets.token_hex(44),"CRYPTO_SECRET":secrets.token_hex(44)}
redis=app=holder=None; cluster_up=False; httpd=None; checkpoints=[]; requests=[]; task_ids=[]; pg_log=work/"pg.log"; stage=""
cols=("username","model_name","use_group","node_name")
rowlist=[]; streams=[]; money=[]; logcalls=[]; version="v1.0.0-rc.51.chuanapi.im."
report={"status":"running","labels":[],"checkpoints":checkpoints,"stages":streams,"synthetic":True}
def save():
    (run/"receipt.json").write_text(json.dumps(report,ensure_ascii=False,indent=2)+"\n")
def event(k,**details):
    checkpoints.append({"step":k,**details}); print(k,json.dumps(details,ensure_ascii=False),flush=True); save()
def child(cmd, *, db=None, input=None, moreenv=None):
    p=sp.run(list(map(str,cmd)),input=input,text=True,capture_output=True, env={**env,**({"PGDATABASE":db} if db else {}),**(moreenv or {})},timeout=60)
    if p.returncode:
        raise RuntimeError(str(cmd[0])+" failed: "+p.stderr.strip()[:1000])
    return p.stdout.strip()
ports=set()
def port():
    while True:
        with socket.socket() as s:
            s.bind(("127.0.0.1",0)); p=s.getsockname()[1]
        if p not in ports: ports.add(p); return p
def sql(q,db="main"):
    return child([a.pg_bin/"psql","-XAtq","-v","ON_ERROR_STOP=1"],input=q,db=db)
def pg(tool,*args):
    return child([a.pg_bin/tool,*args])
def timing(start):return round((time.monotonic()-start)*1000,2)
def until(check,why,timeout=35):
    end=time.monotonic()+timeout
    while time.monotonic()<end:
        if check():return
        time.sleep(.07)
    raise AssertionError("timed out: "+why)
def sha(path):
    with path.open("rb") as f:return hashlib.file_digest(f,"sha256").hexdigest()
def services():
    global cluster_up,redis,redis_url
    env["PGPORT"]=str(port());pwfile=work/"pgpw";pwfile.write_text(env["PGPASSWORD"]+"\n")
    pg("initdb","-D",work/"pg","-U",env["PGUSER"],"--pwfile",pwfile,"--auth=scram-sha-256","--no-locale","-E","UTF8")
    with (work/"pg"/"postgresql.conf").open("a") as f:
        f.write("\nlog_line_prefix='%a|%d|%p| '\nlog_statement='ddl'\nlog_duration=off\nlog_min_duration_statement=0\n")
    pg("pg_ctl","-D",work/"pg","-w","-l",pg_log,"-o",f"-h 127.0.0.1 -p {env['PGPORT']} -c unix_socket_directories='' -c shared_buffers=32MB -c io_method=sync","start");cluster_up=True
    assert sql("SHOW server_version_num","postgres")=="180006"
    for db in ("main","logdb"):pg("createdb",db)
    rport=port();redis_url=f"redis://:{rpw}@127.0.0.1:{rport}/0";cfg=work/"redis.conf"
    cfg.write_text(f'bind 127.0.0.1\nprotected-mode yes\nport {rport}\nrequirepass {rpw}\nsave ""\nappendonly no\ndir "{work}"\n')
    redis=sp.Popen([a.redis_bin/"redis-server",cfg],stdout=(work/"redis.log").open("w"),stderr=sp.STDOUT,env=env)
    def cli(*args):return child([a.redis_bin/"redis-cli","--raw","-h","127.0.0.1","-p",rport,*args],moreenv={"REDISCLI_AUTH":rpw})
    def ready():
        assert redis.poll() is None
        try:return cli("PING")=="PONG"
        except RuntimeError:return False
    until(ready,"private redis")
    info=lambda:dict(l.split(":",1) for l in cli("INFO","server").splitlines() if ":" in l)
    ri=info();assert ri["redis_version"]=="8.8.0" and cli("DBSIZE")=="0"
    return sql("select system_identifier from pg_control_system()","postgres"),ri["run_id"],info
def dsn(db,label):
    return f"postgresql://{env['PGUSER']}:{env['PGPASSWORD']}@127.0.0.1:{env['PGPORT']}/{db}?sslmode=disable&application_name={label}"
opener=request.build_opener(request.ProxyHandler({}))
def http(method,path,payload=None,auth=None):
    headers={"Content-Type":"application/json"}
    if auth:headers['Authorization']="Bearer "+auth
    rq=request.Request(base+path,None if payload is None else json.dumps(payload).encode(),headers,method=method)
    try:
        with opener.open(rq,timeout=12) as r: return r.status,r.headers,r.read()
    except error.HTTPError as e:return e.code,e.headers,e.read()
def api(method,path,payload=None,auth=None):
    code,_,body=http(method,path,payload,auth);data=json.loads(body)
    assert code==200 and data.get("success"),(method,path,code,data.get("message"))
    return data.get("data")
def launch(exe,label,want,guard=False,release=None):
    global app,stage,base
    stage=label;begin=time.monotonic(); p=port();base=f"http://127.0.0.1:{p}"
    logfile=work/(label+".log")
    app=sp.Popen([exe,"--log-dir",str(work/(label+"_logs"))],cwd=work,stdout=logfile.open("w"),stderr=sp.STDOUT,env={
        **env,**session_vars,"SQL_DSN":dsn('main',label),"LOG_SQL_DSN":dsn('logdb',label),"REDIS_CONN_STRING":redis_url,
        "NODE_TYPE":"master","PORT":str(p),"HTTP_BIND_ADDRESS":"127.0.0.1","SYNC_FREQUENCY":"2","GIN_MODE":"release","GOMAXPROCS":"1",
        "MEMORY_CACHE_ENABLED":"true","GLOBAL_API_RATE_LIMIT_ENABLE":"false","HTTP_PROXY":"http://127.0.0.1:1","HTTPS_PROXY":"http://127.0.0.1:1","NO_PROXY":"127.0.0.1,localhost",
        **({"STARTUP_MIGRATIONS":"verify-only"} if guard else {})})
    if release:release()
    def ready():
        if app.poll() is not None:
            lines=logfile.read_text(errors="replace").splitlines();raise RuntimeError(label+" exited: "+" ".join(lines[-7:])[:1000])
        try:
            api("GET","/api/readyz");return True
        except (OSError,AssertionError,ValueError):return False
    until(ready,"real gateway "+label,60)
    assert api("GET","/api/status")["version"]==want
    assert "Redis is enabled" in logfile.read_text()
    event("ready",phase=label,version=want,ms=timing(begin),mode="verify-only" if guard else "default")
def stop_app():
    global app
    if app is not None:
        if app.poll() is None:
            app.terminate()
            try:app.wait(25)
            except sp.TimeoutExpired:app.kill();app.wait();raise
        app=None
class Mock(BaseHTTPRequestHandler):
    def do_POST(self):
        p=json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        if self.path!="/v1/chat/completions" or p.get("model")!="qa-upgrade" or not p.get("stream"):
            self.send_error(400);return
        logcalls.append(stage)
        def chunk(choices,usage=None):
            o={"id":"chatcmpl-local","object":"chat.completion.chunk","created":1700000000,"model":"qa-upgrade","choices":choices}
            if usage:o["usage"]=usage
            return ("data: "+json.dumps(o)+"\n\n").encode()
        chunks=[chunk([{"index":0,"delta":{"role":"assistant","content":"local "+stage},"finish_reason":None}]),
            chunk([{"index":0,"delta":{},"finish_reason":"stop"}]),
            chunk([],{"prompt_tokens":9,"completion_tokens":2,"total_tokens":11}),b"data: [DONE]\n\n"]
        self.send_response(200);self.send_header("Content-Type","text/event-stream");self.end_headers()
        for x in chunks:self.wfile.write(x);self.wfile.flush()
    def log_message(self,*args):pass
def schema(db):
    b=child([a.pg_bin/"pg_dump","--schema-only","--no-owner","--no-acl","--restrict-key=QaOnly",'-d',db])
    return hashlib.sha256(b.encode()).hexdigest()
def types():
    rows=sql("select column_name,data_type,coalesce(character_maximum_length,0) from information_schema.columns where table_schema='public' and table_name='quota_data' and column_name in ('username','model_name','use_group','node_name') order by 1")
    return {r.split('|')[0]:r.split('|')[1:] for r in rows.splitlines()}
def seed_labels(long):
    for i,col in enumerate(cols):
        v=["u","m","g","n"];n=(76+i*11) if long else 64
        tag=('wide' if long else 'old')+col+'-'; end=f'-終{i}';v[i]=tag+'界'*(n-len(tag)-len(end))+end
        ident=9000+int(long)*10+i
        sql(f"insert into quota_data(user_id,username,model_name,use_group,node_name,created_at,count,quota) values ({ident},"+",".join("'"+x+"'" for x in v)+",3600,1,7)")
        rowlist.append([ident,*v]);report['labels'].append({"field":col,"length":n,"record":ident,"sha256":hashlib.sha256(v[i].encode()).hexdigest()})
def relation_size():
    n,b,h=map(int,sql("select (select count(*) from quota_data),pg_total_relation_size('quota_data'),pg_relation_filenode('quota_data')").split('|'))
    return dict(rows=n,bytes_including_indexes=b,heap_filenode=h)
def check_labels():
    got=json.loads(sql("select coalesce(json_agg(json_build_array(user_id,username,model_name,use_group,node_name) order by user_id),'[]') from quota_data where user_id between 9000 and 9013"))
    assert got==rowlist,"report label content changed, not just length"
    event("full-field-content",phase=stage,rows=len(rowlist),types=types())
def configure():
    global admin,key
    api('POST','/api/setup',{'username':'upgraderoot','password':loginpw,'confirmPassword':loginpw})
    admin=api('POST','/api/user/login',{'username':'upgraderoot','password':loginpw})['access_token']
    for k,v in [('GroupRatio',{'default':1,'vip':1,'svip':1}),('UserUsableGroups',{'default':'default','auto':'auto'}),('AutoGroups',['default']),('ModelPrice',{'qa-upgrade':0.0001})]:
        api('PUT','/api/option/',{'key':k,'value':json.dumps(v)},admin)
    api('POST','/api/channel/',{'mode':'single','channel':{'type':1,'name':'fixture-only','base_url':'http://127.0.0.1:'+str(httpd.server_port),'key':'fixture-only','status':1,'auto_ban':0,'group':'default','models':'qa-upgrade'}},admin)
    api('POST','/api/token/',{'name':'fixture-token','group':'auto','unlimited_quota':False,'remain_quota':100000,'cross_group_retry':True},admin)
    tok=api('GET','/api/token/',auth=admin)['items'][0];key=api('POST',f"/api/token/{tok['id']}/key",auth=admin)['key']
    if not key.startswith('sk-'):key='sk-'+key
def wallet():
    return list(map(int,sql("select quota,quota_paid,quota_bonus,quota_reward,used_quota,request_count from users where id=1").split('|')))
def token_wallet():
    return list(map(int,sql("select remain_quota,used_quota from tokens where name='fixture-token'").split('|')))
def evidence(r):
    # Entire rows are stable through a stop/start; retain hashes, not request contents.
    q=lambda table: f"select coalesce(jsonb_agg(to_jsonb(t) order by id)::text,'[]') from {table} t where request_id='{r}'"
    return {t:hashlib.sha256(sql(q(t),db).encode()).hexdigest() for t,db in [('logs','logdb'),('balance_ledger','main')]}
def relay():
    before=wallet();tb=token_wallet();calls=len(logcalls)
    code,h,b=http("POST","/v1/chat/completions",{"model":"qa-upgrade","messages":[{"role":"user","content":"synthetic local stream"}],"stream":True,"stream_options":{"include_usage":True}},key)
    assert code==200 and h.get_content_type()=="text/event-stream",(code,b[:350])
    records=[s[6:] for s in b.decode().splitlines() if s.startswith("data: ")]
    assert records[-1]=="[DONE]" and len(logcalls)==calls+1 and logcalls[-1]==stage
    chunks=[json.loads(x) for x in records[:-1]]
    assert "".join(c.get("delta",{}).get("content","") or "" for x in chunks for c in x["choices"])=="local "+stage
    assert any(x.get("usage",{}).get("total_tokens")==11 for x in chunks if x.get("usage"))
    rid=h['X-Oneapi-Request-Id'];assert re.fullmatch(r'[\w-]+',rid) and rid not in requests
    q=f"select quota,prompt_tokens,completion_tokens,is_stream from logs where type=2 and request_id='{rid}'"
    until(lambda:bool(sql(q,"logdb")),"logged "+stage)
    row=sql(q,"logdb");cost,p,c,stream=row.split('|');cost=int(cost);assert (cost,int(p),int(c),stream)==(50,9,2,"t")
    expected=[before[0]-cost,before[1]-cost,before[2],before[3],before[4]+cost,before[5]+1]
    until(lambda:wallet()==expected,'wallet settlement '+stage)
    ta=[tb[0]-cost,tb[1]+cost]
    until(lambda:token_wallet()==ta,'token settlement '+stage)
    net=sql(f"select count(*),sum(delta) from balance_ledger where user_id=1 and request_id='{rid}'")
    nr,delta=map(int,net.split('|')); assert nr>0 and delta==-cost
    requests.append(rid);money.append(cost)
    streams.append({"stage":stage,"request":rid,"quota":cost,"wallet_after":expected,"ledger_net":delta,"ledger_rows":nr,"token_quota_after":ta,"evidence_hashes":evidence(rid),"tokens":[9,2]})
    event("charged-stream",phase=stage,request=rid,cost=cost)
def history():
    if not requests:return
    where="("+",".join("'"+r+"'" for r in requests)+")"
    logs=sql(f"select count(*),sum(quota),count(distinct request_id) from logs where type=2 and request_id in {where}","logdb")
    assert logs==f"{len(requests)}|{sum(money)}|{len(requests)}"
    assert token_wallet()==streams[-1]['token_quota_after']
    for record,r,cost in zip(streams,requests,money):
        assert evidence(r)==record['evidence_hashes'], 'old log or ledger changed: '+r
        assert sql(f"select sum(delta) from balance_ledger where request_id='{r}'")==str(-cost)
    w=wallet();assert w[0]==sum(w[1:4]) and w==streams[-1]["wallet_after"],"wallet changed on restart"
    assert sql("select count(*) from balance_ledger where balance_before+delta<>balance_after")=="0"
def task():
    # The target is 2 seconds after Unix epoch: only our known marker qualifies.
    marker="rehearsal-marker-"+stage
    sql(f"insert into logs(created_at,type,content) values(1,1,'{marker}')","logdb")
    obj=api("POST","/api/system-task/log-cleanup?target_timestamp=2",auth=admin)
    tid=obj["task_id"];assert re.fullmatch(r'[\w-]+',tid) and tid not in task_ids;task_ids.append(tid)
    def finished():
        x=api("GET","/api/system-task/"+tid,auth=admin)
        assert x["status"]!="failed",x.get("error")
        if x["status"]!="succeeded":return False
        assert x["type"]=="log_cleanup" and x["result"]["deleted_count"]==1
        return True
    until(finished,"master task "+stage,45)
    assert sql(f"select count(*) from logs where content='{marker}'","logdb")=="0"
    event("master-task",phase=stage,task_id=tid,deleted_synthetic_markers=1)
def acquire_read_lock():
    global holder
    holder=sp.Popen([a.pg_bin/"psql","-XAtq","-v","ON_ERROR_STOP=1","-d","main"],stdin=sp.PIPE,stdout=sp.PIPE,stderr=sp.DEVNULL,env=env,text=True)
    holder.stdin.write("BEGIN; LOCK TABLE quota_data IN ACCESS SHARE MODE; SELECT 'held';\n");holder.stdin.flush()
    assert holder.stdout.readline().strip()=="held"
def release_when_blocked():
    q="""select count(*) from pg_locks l join pg_stat_activity a using(pid) where a.application_name='target' and l.relation='public.quota_data'::regclass and not l.granted and l.mode='AccessExclusiveLock'"""
    until(lambda:sql(q)!='0','target widening DDL waiting exclusively on quota_data',45)
    begin=time.monotonic();time.sleep(.25)
    holder.stdin.write("COMMIT;\n\\q\n");holder.stdin.flush();holder.wait(8)
    event("observed-exclusive-lock-wait",controlled_hold_after_observed_ms=timing(begin))
def close(proc):
    if proc and proc.poll() is None:
        proc.terminate()
        try:proc.wait(8)
        except sp.TimeoutExpired:proc.kill();proc.wait()
def seed_fee():
    trade="CIF"+"a"*29
    fingerprint=hashlib.sha256(b'epay\0fixture-receipt').hexdigest()
    # Synthetic evidence only: do not create/mimic a real merchant callback.
    sql(f"""BEGIN;
    insert into agent_payment_receipts(receipt_key,provider,provider_reference,merchant_scope,environment,purpose,local_order_key,owner_user_id,currency,currency_exponent,paid_minor,paid_at,verified_at)
    values('{fingerprint}','epay','fixture-receipt','fixture-shop','live','invoice_fee','{trade}',1,'CNY',2,12,1001,1002);
    insert into customer_invoice_applications(user_id,idempotency_key,request_hash,merchant_scope,status,amount_minor,fee_state,fee_rail,fee_paid_minor,fee_paid_at,fee_trade_no,fee_payment_method,fee_gateway,fee_receipt_id,fee_minor,fee_rate_bps,order_count,invoice_type,title_type,title,tax_id,category,email,address,phone,bank_name,bank_account,reason,action_by,issued_at,document_sha256,reconciliation_reason,reconciliation_by,reconciliation_at)
    select 1,'fixture-request','{fingerprint}','fixture-shop','pending',1200,'paid','epay',12,1001,'{trade}','wxpay','https://fixture.invalid/',id,12,100,1,'ordinary','company','fixture','','test','fixture@example.invalid','','','','','',0,0,'','',0,0
    from agent_payment_receipts where receipt_key='{fingerprint}'; COMMIT;""")
def fee_digest():
    return sql("select md5(to_jsonb(a)::text||to_jsonb(r)::text) from customer_invoice_applications a join agent_payment_receipts r on a.fee_receipt_id=r.id where r.purpose='invoice_fee' and r.provider_reference='fixture-receipt'")
def ddl_details():
    text=pg_log.read_text(errors="replace")
    # Capture server-measured statement duration (includes intentional wait), never log SQL literals.
    durations=[];pending={}; trace=[]
    # log_statement=ddl prints the SQL *before* it runs; PostgreSQL then logs a
    # duration without repeating the SQL. Match same backend PID; never add
    # parse/bind durations or pick up another backend's SQL.
    for line in text.splitlines():
        m=re.match(r'^target\|main\|(\d+)\|\s*LOG:\s*(.*)',line)
        if not m:continue
        pid,body=m.groups()
        stmt=re.search(r'(?:^|\s)(?:execute [^:]*:|statement:)\s*(.*)$',body,re.I)
        duration=re.match(r'duration:\s*([.\d]+) ms(?:\s|$)',body)
        if stmt:
            pending.pop(pid,None)
            c=re.search(r'ALTER TABLE\s+"?(?:public\.)?quota_data"?\s+ALTER COLUMN\s+"?(username|model_name|use_group|node_name)"?\s+(?:SET DATA TYPE|TYPE)\s+text\b',stmt.group(1),re.I)
            if c:pending[pid]=c.group(1);trace.append({"column":c.group(1),"pid":pid,"with_duration":bool(duration)})
        if duration and pid in pending and (stmt or re.fullmatch(r'duration:\s*[.\d]+ ms\s*',body)):
            durations.append({"column":pending.pop(pid),"ms":float(duration.group(1))})
    assert len(durations)==4 and {d['column'] for d in durations}==set(cols),(durations,trace)
    patched_ddl=[l for l in text.splitlines() if l.startswith("rollback|") and re.search(r'(?:statement:|execute [^:]*:)\s*(?:ALTER|CREATE|DROP|TRUNCATE|REINDEX)\s+',l,re.I)]
    assert not patched_ddl,"rollback attempted DDL even if the end-state matched"
    return durations
try:
    for name,path,ver in [('stock',a.im13,version+'13'),('target',a.im18,version+'18')]:
        assert child([path,"--version"])==ver
        event("artifact",role=name,version=ver,sha256=sha(path))
    sysid,rid,rinfo=services()
    event("isolated-services",postgres=sql("show server_version"),pg_system_id=sysid,redis_version=rinfo()['redis_version'],redis_run_id=rid)
    httpd=ThreadingHTTPServer(('127.0.0.1',0),Mock);threading.Thread(target=httpd.serve_forever,daemon=True).start()
    launch(a.im13,"stock",version+"13");assert types()=={k:["character varying","64"] for k in cols},types()
    configure();seed_labels(False);check_labels();relay();task();history();stop_app()
    if a.padding_rows:
        sql(f"insert into quota_data(user_id,username,model_name,use_group,node_name,created_at,count,quota) select 1000000+i,'fixture-'||i,'model','group','node',3600,1,7 from generate_series(1,{a.padding_rows}) i")
    event("before-widening",**relation_size())
    acquire_read_lock();launch(a.im18,"target",version+"18",release=release_when_blocked)
    assert api("GET","/api/user/self",auth=admin)["username"]=="upgraderoot"
    assert types()=={k:["text","0"] for k in cols},types();check_labels();history();event("after-widening",**relation_size())
    seed_labels(True);seed_fee();fd=fee_digest();assert re.fullmatch(r"[a-f0-9]{32}",fd);check_labels()
    relay();task();history();stop_app()
    schemas={db:schema(db) for db in ('main','logdb')};event("target-schema",hashes=schemas,fee_evidence_digest=fd)
    if not a.rollback.exists() and a.wait_binaries:
        print("awaiting rollback binary",flush=True);until(a.rollback.exists,"patched binary ready",a.wait_binaries)
    want=version+"13.rollback.1";assert child([a.rollback,"--version"])==want
    event("artifact",role="rollback",version=want,sha256=sha(a.rollback))
    for exe,label,want,verify in [(a.rollback,"rollback",want,True),(a.im18,"reup",version+"18",False)]:
        launch(exe,label,want,guard=verify)
        assert sql("select system_identifier from pg_control_system()","postgres")==sysid and rinfo()["run_id"]==rid
        assert api("GET","/api/user/self",auth=admin)["username"]=="upgraderoot"
        assert types()=={k:["text","0"] for k in cols};check_labels();history()
        assert {db:schema(db) for db in schemas}==schemas and fee_digest()==fd
        relay();task();history();stop_app()
    assert len(set(task_ids))==4 and len(requests)==4
    assert sql("select count(*) from system_tasks where status='succeeded' and task_id in ("+','.join("'"+x+"'" for x in task_ids)+")")=="4"
    assert sql("select count(*) from logs where type=2","logdb")=="4"
    assert len(logcalls)==4 and all(x==50 for x in money)
    durations=ddl_details()
    report.update(status="PASS",widening_pg_statement_ms=durations,rollback_ddl_count=0,total_quota=sum(money),schema_sha256=schemas,invoice_fee_unchanged=True)
    event("PASS",total_charges=sum(money),streams=len(requests),tasks=len(task_ids),evidence=str(run))
except BaseException as exc:
    report.update(status="FAIL",error=str(exc)[:1400]);save();raise
finally:
    close(holder)
    try:stop_app()
    finally:
        if httpd:httpd.shutdown();httpd.server_close()
        close(redis)
        if cluster_up:pg("pg_ctl","-D",work/"pg","-m","fast","-w","stop")
        tmp.cleanup()
