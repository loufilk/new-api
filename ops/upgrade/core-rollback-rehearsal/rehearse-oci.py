#!/usr/bin/env python3
"""Run the unchanged Linux rollback acceptance against preloaded digest-pinned local Docker images."""
import argparse, hashlib, json, os, platform, re, secrets, shutil, signal, subprocess as sp, sys, tempfile
from pathlib import Path

FLAG = "io.chuanapi.rollback-rehearsal"
PIN = re.compile(r"(?:sha256:[0-9a-f]{64}|[a-z0-9][a-z0-9._:/-]*@sha256:[0-9a-f]{64})")
# None of these variable values go on the Docker CLI argument list.
APPCONF = ("SQL_DSN", "LOG_SQL_DSN", "REDIS_CONN_STRING", "SESSION_SECRET", "CRYPTO_SECRET",
           "NODE_TYPE", "PORT", "HTTP_BIND_ADDRESS", "SYNC_FREQUENCY", "GIN_MODE", "GOMAXPROCS",
           "MEMORY_CACHE_ENABLED", "GLOBAL_API_RATE_LIMIT_ENABLE", "HTTP_PROXY", "HTTPS_PROXY",
           "NO_PROXY", "STARTUP_MIGRATIONS")

def checked(cmd,**kw):
    return sp.check_output(cmd,text=True,timeout=30,**kw).strip()

def write_json(path,obj):
    path.write_text(json.dumps(obj,indent=2)+"\n")

def internal(config,role,args):
    cfg=json.loads(Path(config).read_text()); idx=cfg['artifacts'][role]; group=cfg['group']
    version_check=args==['--version']
    if not version_check and (len(args)!=2 or args[0]!='--log-dir'):
        raise ValueError("unsupported harness invocation")
    if not version_check and (os.environ.get('HTTP_BIND_ADDRESS')!='127.0.0.1' or os.environ.get('NODE_TYPE')!='master'):
        raise ValueError("only loopback master test processes are supported")
    name=group+'-'+role+'-'+secrets.token_hex(4)
    # Use content ID after inspecting the original pin, so a tag can never move.
    cmd=[*cfg['cli'],'run','--rm','--pull=never','--name',name,'--label',FLAG+'='+group,
         '--read-only','--cap-drop=ALL','--security-opt=no-new-privileges','--user',f'{os.getuid()}:{os.getgid()}',
         '--network','none' if version_check else 'host','--cpus=1','--pids-limit=256','--stop-timeout=15',
         '--tmpfs','/data:rw,nosuid,nodev,noexec,mode=1777,size=64m',
         '--tmpfs','/tmp:rw,nosuid,nodev,noexec,mode=1777,size=64m']
    if not version_check:
        for key in APPCONF:
            if key in os.environ:cmd.extend(['--env',key])
    cmd += [idx['id'],*(['--version'] if version_check else ['--log-dir','/tmp/qa-logs'])]
    # Only allow our own explicitly local daemon selector. No host .docker config,
    # proxy credentials, raw DSNs or secrets appear in saved artifacts.
    clientenv={'PATH':os.environ['PATH'],'HOME':str(Path(config).parent)}
    clientenv.update({key:os.environ[key] for key in APPCONF if key in os.environ and not version_check})
    stopped=False
    def shutdown(_sig,_frame):
        nonlocal stopped
        if not stopped and not version_check:
            stopped=True
            sp.run([*cfg['cli'],'stop','-t','15',name],env=clientenv,capture_output=True,timeout=18,check=True)
    signal.signal(signal.SIGTERM,shutdown)
    proc=sp.Popen(cmd,env=clientenv)
    rc=proc.wait()
    if not version_check:
        write_json(Path(config).parent/(name+'.stop'),{'role':role,'graceful_signal':stopped,'exit':rc})
    return rc

def run():
    p=argparse.ArgumentParser(description=__doc__)
    for k in ('im13','im18','rollback','pg-bin','redis-bin','output'):p.add_argument('--'+k,required=True)
    for role in ('im13', 'im18', 'rollback'):
        p.add_argument('--'+role+'-revision',required=True)
    p.add_argument('--confirm-dedicated-test-host',action='store_true')
    p.add_argument('--docker-socket',default='/var/run/docker.sock',type=Path,
                   help="Local Unix socket only, e.g. a rootless Docker socket.")
    p.add_argument('--padding-rows',default=256,type=int)
    a=p.parse_args()
    if os.getuid()==0 or sys.platform!='linux':p.error("run as an unprivileged user on a dedicated Linux test host")
    pins={'stock':a.im13,'target':a.im18,'rollback':a.rollback}
    if not all(PIN.fullmatch(v) for v in pins.values()):p.error("all images must be full immutable sha256 image IDs or repo@sha256 digests; no tags")
    revisions={'stock':a.im13_revision,'target':a.im18_revision,'rollback':a.rollback_revision}
    if not all(re.fullmatch(r'[0-9a-f]{40}', v) for v in revisions.values()):p.error("each source revision must be a full lowercase git SHA")
    if not a.confirm_dedicated_test_host:p.error("explicit dedicated non-production test host confirmation is required")
    if not a.docker_socket.is_absolute() or not a.docker_socket.is_socket():p.error("supply an existing local Docker Unix socket")
    dock=shutil.which('docker')
    if not dock:p.error("Docker CLI is not installed; no installation or pull will be attempted")
    if any(os.environ.get(k) for k in ['SQL_DSN','LOG_SQL_DSN','REDIS_CONN_STRING']):p.error("unset preexisting store DSNs")
    cli=[dock,'--host','unix://'+str(a.docker_socket)]
    os.umask(0o077);output=Path(a.output).resolve();output.mkdir(parents=True,exist_ok=True)
    dst=Path(tempfile.mkdtemp(prefix='oci-evidence-',dir=output))
    group='chuanqa-'+secrets.token_hex(12)
    report={'status':'FAIL','mode':'OCI on dedicated Linux test host','role_artifacts':{},
            'note':'Inner receipt artifact SHA-256 values are shim hashes. Actual inspected image IDs and pins are here.'}
    core=Path(__file__).with_name('rehearse.py').resolve();report['core_harness_sha256']=hashlib.sha256(core.read_bytes()).hexdigest()
    tmp=tempfile.TemporaryDirectory(prefix='im18-oci-wrappers-');work=Path(tmp.name);clientenv={'HOME':str(work),'PATH':os.environ['PATH']}
    try:
        server=json.loads(checked([*cli,'version','--format','{{json .Server}}'],env=clientenv))
        if server.get('Os')!='linux':raise ValueError('local Linux Docker daemon required')
        arch={'x86_64':'amd64','aarch64':'arm64'}.get(platform.machine(),platform.machine())
        for role,pin in pins.items():
            img=json.loads(checked([*cli,'image','inspect',pin],env=clientenv))[0];iid=img['Id']
            if not re.fullmatch(r'sha256:[a-f0-9]{64}',iid) or img.get('Os')!='linux' or img.get('Architecture')!=arch:raise ValueError(role+': image must be native Linux architecture')
            if pin.startswith('sha256:') and iid!=pin:raise ValueError('local image ID mismatch')
            if not pin.startswith('sha256:') and not any(s.endswith('@'+pin.split('@')[1]) for s in img.get('RepoDigests') or []):raise ValueError(role+': repo digest mismatch')
            labels=img['Config'].get('Labels') or {}
            if labels.get('org.opencontainers.image.revision')!=revisions[role]:raise ValueError(role+': expected source revision does not match')
            if role=='rollback' and labels.get('io.chuanapi.release.mode')!='im13-schema-preserving-rollback':raise ValueError('expected the dedicated rollback image')
            report['role_artifacts'][role]={'id':iid,'pin':pin,'architecture':arch,'label_revision':labels.get('org.opencontainers.image.revision'),
                'label_version':labels.get('org.opencontainers.image.version'),'entrypoint':img['Config'].get('Entrypoint')}
        cfg=work/'config.json';write_json(cfg,{'cli':cli,'group':group,'artifacts':report['role_artifacts']})
        for role in pins:
            f=work/role
            f.write_text('#!'+sys.executable+'\nimport os, sys\nos.execv(sys.executable, [sys.executable,'+repr(str(Path(__file__).resolve()))+', "--internal",'+repr(str(cfg))+','+repr(role)+',*sys.argv[1:]])\n')
            f.chmod(0o700)
        cmd=[sys.executable,str(core),'--im13',str(work/'stock'),'--im18',str(work/'target'),'--rollback',str(work/'rollback'),
             '--pg-bin',str(Path(a.pg_bin).resolve()),'--redis-bin',str(Path(a.redis_bin).resolve()),'--output',str(dst),'--padding-rows',str(a.padding_rows)]
        proc=sp.Popen(cmd,start_new_session=True)
        try:rc=proc.wait()
        except BaseException:
            proc.send_signal(signal.SIGINT);proc.wait(timeout=90);raise
        if rc:raise RuntimeError('inner functional rehearsal failed: '+str(rc))
        receipt,=dst.glob('evidence-*/receipt.json')
        if json.loads(receipt.read_text()).get('status')!='PASS':raise RuntimeError('inner acceptance incomplete')
        stops=[json.loads(f.read_text()) for f in sorted(work.glob('*.stop'))]
        if sorted(s['role'] for s in stops)!=['rollback','stock','target','target'] or any(not s['graceful_signal'] or s['exit'] for s in stops):
            raise RuntimeError('expected exactly four cleanly stopped gateway containers')
        report.update(status='PASS',inner_receipt=str(receipt.relative_to(dst)),inner_receipt_sha256=hashlib.sha256(receipt.read_bytes()).hexdigest(),stops=stops)
    finally:
        try:
            leftovers=checked([*cli,'ps','-aq','--filter','label='+FLAG+'='+group],env=clientenv).split()
            if leftovers:
                report['status']='FAIL'
                # Unpredictable per-run label, IDs enumerated locally. Never target stock containers.
                checked([*cli,'rm','-f',*leftovers],env=clientenv)
                raise RuntimeError("leftover rehearsal containers were removed; not a clean pass")
        except BaseException:
            report['status']='FAIL';raise
        finally:
            tmp.cleanup();write_json(dst/'oci-attestation.json',report);print("OCI attestation:",dst/'oci-attestation.json',flush=True)
    return 0

if __name__=='__main__':
    if sys.argv[1:2]==['--internal']:sys.exit(internal(sys.argv[2],sys.argv[3],sys.argv[4:]))
    sys.exit(run())
