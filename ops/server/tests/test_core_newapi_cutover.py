"""Exercise release safety gates without Docker, network or production credentials."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().parents[2] / 'upgrade/core-newapi-cutover.sh'
FROM = 'sha256:' + 'a' * 64
TO = 'sha256:' + 'b' * 64
REVISION = 'c' * 40
DOCKER = r'''#!/usr/bin/env python3
import json, os, pathlib, sys
args = sys.argv[1:]
with open(os.environ['TRACE'], 'a', encoding='utf-8') as log:
    print(json.dumps(args), file=log)
if args[:2] == ['image', 'inspect']:
    field, ref = args[3:]
    if 'revision' in field: print(os.environ['CORE_TO_REVISION'])
    elif 'release.mode' in field: print(os.getenv('TARGET_MODE', ''))
    elif field == '{{.Id}}': print(ref)
    elif field == "{{.Os}}/{{.Architecture}}": print("linux/amd64")
    else: sys.exit(3)
elif args[0] == 'inspect':
    state = pathlib.Path(os.environ['STATE'])
    switched = state.is_file()
    field = args[2]
    if field == '{{.Image}}': print(os.environ['CORE_TO_IMAGE' if switched else 'CORE_FROM_IMAGE'])
    elif field == '{{.State.ExitCode}}': print(os.getenv('STOP_EXIT', '0'))
    elif field == '{{.State.Status}}': print(os.getenv('SOURCE_STATE', 'running'))
    else: sys.exit(3)
elif args[0] == 'compose':
    if 'ps' in args:
        print('target-gateway' if pathlib.Path(os.environ['STATE']).is_file() else 'source-gateway')
    elif 'up' in args:
        pathlib.Path(os.environ['STATE']).touch()
    elif 'exec' in args:
        print('{"success":true}')
    elif not any(cmd in args for cmd in ('config', 'stop', 'rm')):
        sys.exit(3)
else: sys.exit(3)
'''


class TestCutover(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        docker = self.root / 'docker'
        docker.write_text(DOCKER)
        docker.chmod(0o755)
        self.pin = self.root / 'pinned.yml'
        self.trace = self.root / 'trace.jsonl'
        self.env = {
            **os.environ, 'PATH': f'{self.root}:{os.environ["PATH"]}',
            'TRACE': str(self.trace), 'STATE': str(self.root / 'state'),
            'CORE_FROM_IMAGE': FROM, 'CORE_TO_IMAGE': TO, 'CORE_TO_REVISION': REVISION,
            'CORE_CUTOVER_PIN_FILE': str(self.pin),
            'CORE_TRAFFIC_FENCED': 'yes', 'CORE_OTHER_WRITERS_STOPPED': 'yes',
            'CORE_INVOICES_RECONCILED': 'yes', 'CORE_EXECUTE': 'no',
            'CORE_BACKUP_VERIFIED': 'yes',
            'TARGET_MODE': 'im13-schema-preserving-rollback',
        }

    def run_script(self, phase, **env):
        result = subprocess.run(
            ['bash', str(SCRIPT), phase, '-p', 'qa', '-f', str(self.root / 'base.yml')],
            env={**self.env, **env}, text=True, capture_output=True, timeout=12,
        )
        events = [json.loads(line) for line in self.trace.read_text().splitlines()] if self.trace.exists() else []
        return result, [e for e in events if e[0] == 'compose']

    def test_dry_run_writes_no_pin_and_leaves_service_running(self):
        result, compose = self.run_script('rollback')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(self.pin.exists())
        self.assertTrue(any('config' in e for e in compose))
        self.assertFalse(any(any(cmd in e for cmd in ('stop', 'rm', 'up')) for e in compose))

    def test_stock_im13_is_not_a_rollback_target(self):
        result, compose = self.run_script('rollback', TARGET_MODE='', CORE_EXECUTE='yes')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('refusing stock im.13', result.stderr)
        self.assertEqual(compose, [])

    def test_rollback_requires_invoice_and_traffic_fences(self):
        for unsafe in ('CORE_INVOICES_RECONCILED', 'CORE_TRAFFIC_FENCED', 'CORE_OTHER_WRITERS_STOPPED'):
            with self.subTest(unsafe=unsafe):
                result, compose = self.run_script('rollback', **{unsafe: 'no', 'CORE_EXECUTE': 'yes'})
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(compose, [])

    def test_rollback_persists_master_verify_only_and_recreates_only_api(self):
        result, compose = self.run_script('rollback', CORE_EXECUTE='yes')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('NODE_TYPE: "master"', self.pin.read_text())
        self.assertIn('STARTUP_MIGRATIONS: "verify-only"', self.pin.read_text())
        changes = [e for e in compose if any(cmd in e for cmd in ('stop', 'rm', 'up'))]
        self.assertEqual([next(cmd for cmd in ('stop', 'rm', 'up') if cmd in e) for e in changes], ['stop', 'rm', 'up'])
        self.assertTrue(all(e[-1] == 'new-api' for e in changes))
        self.assertIn('--no-deps', changes[-1])
        self.assertIn(str(self.pin), changes[-1])

    def test_abnormal_drain_stops_before_remove_and_recreate(self):
        result, compose = self.run_script('upgrade', TARGET_MODE='', CORE_EXECUTE='yes', STOP_EXIT='137')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('did not exit cleanly', result.stderr)
        self.assertFalse(self.pin.exists())
        self.assertFalse(any('rm' in e or 'up' in e for e in compose))
