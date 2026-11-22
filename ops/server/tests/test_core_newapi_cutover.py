"""Exercise release safety gates without Docker, network or production credentials."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().parents[2] / 'upgrade/core-newapi-cutover.sh'
STOCK = 'fixture/newapi@sha256:' + 'a' * 64
IM18 = 'fixture/newapi@sha256:' + 'b' * 64
FALLBACK = 'fixture/newapi@sha256:' + 'd' * 64
STOCK_REV = '5c370275b838d37d0ea4480810a2ebc2d7dd49ca'
IM18_REV = 'c' * 40
FALLBACK_REV = '6f7f6fe297624c65d499d9efaa9d9b57191be9b9'
PINS = {'stock': (STOCK, STOCK_REV), 'im18': (IM18, IM18_REV), 'fallback': (FALLBACK, FALLBACK_REV)}
DOCKER = r'''#!/usr/bin/env python3
import json, os, pathlib, sys
args = sys.argv[1:]
with open(os.environ['TRACE'], 'a', encoding='utf-8') as log:
    print(json.dumps(args), file=log)
if args[:2] == ['image', 'inspect']:
    field, ref = args[3:]
    identities=json.loads(os.environ['IDENTITIES'])
    rev,mode=identities[ref]
    direction='FROM' if ref==os.environ['CORE_FROM_IMAGE'] else ('TO' if ref==os.environ['CORE_TO_IMAGE'] else 'STAGED')
    if 'revision' in field: print(os.getenv(direction+'_REVISION_LABEL',rev))
    elif 'release.mode' in field: print(os.getenv(direction+'_MODE',mode))
    elif field == '{{.Id}}': print(ref.split('@')[-1])
    elif '.RepoDigests' in field: print(os.getenv(direction+'_REPO_DIGESTS',ref))
    elif field == "{{.Os}}/{{.Architecture}}": print("linux/amd64")
    else: sys.exit(3)
elif args[0] == 'inspect':
    state = pathlib.Path(os.environ['STATE'])
    switched = state.is_file()
    field = args[2]
    if field == '{{.Image}}': print(os.getenv('LIVE_IMAGE_ID',os.environ['CORE_TO_IMAGE' if switched else 'CORE_FROM_IMAGE'].split('@')[-1]))
    elif field == '{{.State.ExitCode}}': print(os.getenv('STOP_EXIT', '0'))
    elif field == '{{.State.Status}}': print(os.getenv('SOURCE_STATE', 'running'))
    elif 'SHUTDOWN_TIMEOUT_SECONDS' in field: print(os.getenv('SOURCE_DRAIN', ''))
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
            'CORE_APPROVED_STOCK_IMAGE': STOCK, 'CORE_APPROVED_IM18_IMAGE': IM18,
            'CORE_APPROVED_ROLLBACK_IMAGE': FALLBACK, 'CORE_APPROVED_IM18_REVISION': IM18_REV,
            'IDENTITIES': json.dumps({ref: (rev, 'im13-schema-preserving-rollback' if role == 'fallback' else '')
                                      for role, (ref, rev) in PINS.items()}),
            'CORE_CUTOVER_PIN_FILE': str(self.pin),
            'CORE_TRAFFIC_FENCED': 'yes', 'CORE_OTHER_WRITERS_STOPPED': 'yes',
            'CORE_INVOICES_RECONCILED': 'yes', 'CORE_EXECUTE': 'no',
            'CORE_BACKUP_VERIFIED': 'yes',
        }

    def run_script(self, phase, source='im18', target='fallback', **env):
        self.trace.unlink(missing_ok=True)
        source_ref, _ = PINS[source]
        target_ref, target_rev = PINS[target]
        chosen = {'CORE_FROM_IMAGE': source_ref, 'CORE_TO_IMAGE': target_ref, 'CORE_TO_REVISION': target_rev}
        result = subprocess.run(
            ['bash', str(SCRIPT), phase, '-p', 'qa', '-f', str(self.root / 'base.yml')],
            env={**self.env, **chosen, **env}, text=True, capture_output=True, timeout=45,
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
        result, compose = self.run_script('rollback', target='stock', CORE_EXECUTE='yes')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('transition not allowed', result.stderr)
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

    def test_stock_upgrade_rejects_missing_or_wrong_rollback_image_before_stop(self):
        found = json.loads(self.env['IDENTITIES'])
        del found[FALLBACK]
        for invalid in (
            {'IDENTITIES': json.dumps(found)},
            {'STAGED_REVISION_LABEL': 'f' * 40},
            {'STAGED_MODE': ''},
            {'STAGED_REPO_DIGESTS': 'wrong/repo@sha256:' + 'd' * 64},
        ):
            with self.subTest(invalid=invalid):
                result, compose = self.run_script('upgrade', source='stock', target='im18', CORE_EXECUTE='yes', **invalid)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('staged schema-preserving rollback image', result.stderr)
                self.assertEqual(compose, [])
                self.assertFalse(self.pin.exists())

    def test_stop_timeout_leaves_room_for_application_finalization(self):
        for drain, grace in (('', 180), ('000300', 360), ('3601', 180), ('-10', 180)):
            with self.subTest(drain=drain):
                self.pin.unlink(missing_ok=True)
                (self.root / 'state').unlink(missing_ok=True)
                result, compose = self.run_script('rollback', CORE_EXECUTE='yes', SOURCE_DRAIN=drain)
                self.assertEqual(result.returncode, 0, result.stderr)
                stop, = [e for e in compose if 'stop' in e]
                self.assertEqual(stop[stop.index('-t') + 1], str(grace))

    def test_manual_grace_override_cannot_cut_off_configured_drain(self):
        result, compose = self.run_script('rollback', CORE_EXECUTE='yes',
                                          SOURCE_DRAIN='300', CORE_STOP_GRACE_SECONDS='359')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('must be from 360 to 3660', result.stderr)
        self.assertFalse(any('stop' in e for e in compose))
        result, compose = self.run_script('rollback', CORE_EXECUTE='yes',
                                          SOURCE_DRAIN='300', CORE_STOP_GRACE_SECONDS='420')
        self.assertEqual(result.returncode, 0, result.stderr)
        stop, = [e for e in compose if 'stop' in e]
        self.assertEqual(stop[stop.index('-t') + 1], '420')

    def test_abnormal_drain_stops_before_remove_and_recreate(self):
        result, compose = self.run_script('upgrade', source='stock', target='im18', CORE_EXECUTE='yes', STOP_EXIT='137')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('did not exit cleanly', result.stderr)
        self.assertFalse(self.pin.exists())
        self.assertFalse(any('rm' in e or 'up' in e for e in compose))

    def test_only_three_transitions_allowed_before_container_operations(self):
        allowed = {('upgrade', 'stock', 'im18'), ('upgrade', 'fallback', 'im18'), ('rollback', 'im18', 'fallback')}
        for verb in ('upgrade', 'rollback'):
            for source in PINS:
                for target in PINS:
                    with self.subTest(verb=verb, source=source, target=target):
                        result, compose = self.run_script(verb, source=source, target=target)
                        if (verb, source, target) in allowed:
                            self.assertEqual(result.returncode, 0, result.stderr)
                            self.assertTrue(any('config' in e for e in compose))
                        else:
                            self.assertIn('transition not allowed', result.stderr)
                            self.assertEqual(compose, [])
                        self.assertFalse(any(any(cmd in e for cmd in ('stop', 'rm', 'up')) for e in compose))

    def test_im18_to_stock_mislabeled_as_upgrade_is_blocked_in_execute_mode(self):
        result, compose = self.run_script('upgrade', source='im18', target='stock', CORE_EXECUTE='yes')
        self.assertIn('transition not allowed: upgrade im18 -> stock', result.stderr)
        self.assertEqual(compose, [])
        self.assertFalse(self.pin.exists())

    def test_rejects_unapproved_or_wrong_full_revision_and_repo_digest_on_either_side(self):
        cases = (
            {'CORE_FROM_IMAGE': 'unapproved/repo@sha256:' + 'b' * 64},
            {'CORE_TO_IMAGE': 'unapproved/repo@sha256:' + 'd' * 64},
            {'FROM_REVISION_LABEL': IM18_REV[:12]},
            {'TO_REVISION_LABEL': 'a' * 40},
            {'FROM_REPO_DIGESTS': 'other/repo@sha256:' + 'b' * 64},
            {'TO_REPO_DIGESTS': 'other/repo@sha256:' + 'd' * 64},
            {'TO_MODE': ''},
            {'CORE_TO_REVISION': 'a' * 40},
        )
        for kwargs in cases:
            with self.subTest(kwargs=kwargs):
                result, compose = self.run_script('rollback', CORE_EXECUTE='yes', **kwargs)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(compose, [])
                self.assertFalse(self.pin.exists())

    def test_live_container_must_equal_verified_source_before_any_stop(self):
        result, compose = self.run_script('rollback', CORE_EXECUTE='yes', LIVE_IMAGE_ID='sha256:' + 'f' * 64)
        self.assertIn('current image differs', result.stderr)
        self.assertFalse(any(any(cmd in e for cmd in ('stop', 'rm', 'up')) for e in compose))
        self.assertFalse(self.pin.exists())
