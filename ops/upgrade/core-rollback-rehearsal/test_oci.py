"""Test the OCI adapter contract with a fake CLI only. No Docker/network used."""
import json, os, signal, subprocess as sp, sys, tempfile, time, unittest
from pathlib import Path

RUNNER=Path(__file__).with_name('rehearse-oci.py')
class TestLocalOCIAdapter(unittest.TestCase):
    def test_mutable_pin_rejected(self):
        args=[sys.executable,str(RUNNER),'--im13','mutable','--im18','mutable','--rollback','mutable',
              '--im13-revision','a'*40,'--im18-revision','b'*40,'--rollback-revision','c'*40,'--pg-bin','/tmp','--redis-bin','/tmp','--output','/tmp/unused']
        if os.getuid():self.assertIn('sha256',sp.run(args,text=True,capture_output=True).stderr)
    def test_shim_command_and_graceful_stop(self):
        with tempfile.TemporaryDirectory() as t:
            w=Path(t);fake=w/'docker';cfg=w/'config'
            fake.write_text('#!'+sys.executable+'''
import json,os,pathlib,sys,time
d=pathlib.Path(__file__).parent;a=sys.argv[1:]
if a[0]=="stop":
 (d/"stop-args").write_text(json.dumps(a));(d/"finish").touch()
elif a[-1]=="--version":
 (d/"version-args").write_text(json.dumps(a));print("synthetic version")
else:
 (d/"args").write_text(json.dumps(a));(d/"env-check").write_text(json.dumps({"dsn":os.environ.get("SQL_DSN"),"not_allowed":os.environ.get("SYNTHETIC_DO_NOT_PASS")}))
 for _ in range(1000):
  if (d/"finish").exists():break
  time.sleep(.01)
 else:sys.exit(8)
''');fake.chmod(0o700)
            iid='sha256:'+'a'*64
            cfg.write_text(json.dumps({'cli':[str(fake)],'group':'fixture-group','artifacts':{'stock':{'id':iid}}}))
            cmd=[sys.executable,str(RUNNER),'--internal',str(cfg),'stock']
            e=dict(os.environ,HTTP_BIND_ADDRESS='127.0.0.1',NODE_TYPE='master',SQL_DSN='synthetic-only',
                   SYNTHETIC_DO_NOT_PASS='redacted-negative-fixture')
            self.assertEqual(sp.check_output(cmd+['--version'],env=e,text=True).strip(),'synthetic version')
            v=json.loads((w/'version-args').read_text());self.assertEqual(v[v.index('--network')+1],'none');self.assertNotIn('--env',v)
            proc=sp.Popen(cmd+['--log-dir','ignored-fixture-path'],env=e)
            try:
                end=time.monotonic()+5
                while not (w/'env-check').exists() and time.monotonic()<end:time.sleep(.02)
                args=json.loads((w/'args').read_text())
                self.assertEqual(args[args.index('--network')+1],'host');self.assertEqual(args[-3:],[iid,'--log-dir','/tmp/qa-logs'])
                self.assertIn('--pull=never',args);self.assertIn('--read-only',args);self.assertIn('SQL_DSN',args)
                self.assertNotIn('synthetic-only',args);self.assertNotIn('redacted-negative-fixture',args)
                self.assertEqual(json.loads((w/'env-check').read_text()),{'dsn':'synthetic-only','not_allowed':None})
                proc.send_signal(signal.SIGTERM);self.assertEqual(proc.wait(timeout=8),0)
                stop=json.loads((w/'stop-args').read_text());self.assertEqual(stop,['stop','-t','15',args[args.index('--name')+1]])
                item,=w.glob('*.stop');self.assertEqual(json.loads(item.read_text()),{'role':'stock','graceful_signal':True,'exit':0})
            finally:
                if proc.poll() is None:
                    proc.terminate();proc.wait(timeout=8)
if __name__=='__main__':unittest.main()
