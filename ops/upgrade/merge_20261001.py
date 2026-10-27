"""Resolve the reviewed database-candidate merge, refusing unexpected conflicts.

Run only after merging the pinned upstream into a descendant of BASE. This is
an upgrade recipe, not an application startup task. It never writes Git refs.
"""
from pathlib import Path
import json
import re
import subprocess

ROOT = Path('.')
BASE = '62d951fab129f4df1a921735835e5e9c7b8db031'
UPSTREAM = '56758edf95ec162033a6b73b553ac789404f87c9'
PATTERN = re.compile(r'^<<<<<<<[^\n]*\n(.*?)^=======\n(.*?)^>>>>>>>[^\n]*\n', re.M | re.S)


def source(ref, path):
    return subprocess.check_output(['git', 'show', f'{ref}:{path}']).decode()


def replace_once(path, old, new):
    p = ROOT / path
    s = p.read_text()
    if s.count(old) != 1:
        raise RuntimeError(f'{path}: expected one preimage, got {s.count(old)}')
    p.write_text(s.replace(old, new, 1))


policies = {
    'controller/user.go': ['theirs'] * 7,
    'controller/security_enrollment_test.go': ['models'],
    'controller/user_manage_test.go': ['models'],
    'model/task_cas_test.go': ['both'],
    'model/user_update_test.go': ['both', 'tests'],
    'model/main.go': ['access-model', 'ours'],
    'model/user.go': ['ours', 'theirs', 'user-delete'],
    'web/src/components/ai-elements/prompt-input.tsx': ['theirs'],
    'web/src/features/channels/components/__tests__/responses-websocket-setting.test.tsx': ['ours'],
    'web/src/features/dashboard/lib/charts.ts': ['ours', 'theirs', 'theirs'],
    'web/src/features/system-settings/models/__tests__/model-ratio-table-selection.test.tsx': ['ours', 'theirs', 'theirs'],
    'web/src/features/system-settings/models/upstream-price-cells.tsx': ['theirs', 'theirs'],
    'web/src/features/task-plugins/components/plugins-table.tsx': ['ours', 'ours'],
    'web/src/features/task-plugins/__tests__/plugins-table.test.tsx': ['ours', 'ours'],
    'web/src/i18n/static-keys.ts': ['both'],
}
for path, decisions in policies.items():
    p = ROOT / path
    s = p.read_text()
    matches = list(PATTERN.finditer(s))
    if len(matches) != len(decisions):
        raise RuntimeError(f'{path}: unexpected conflict count {len(matches)}')

    def resolve(m):
        decision = decisions.pop(0)
        ours, theirs = m.groups()
        if decision == 'ours':
            return ours
        if decision == 'theirs':
            return theirs
        if decision == 'both':
            return ours + theirs
        if decision == 'tests':
            return ours + '}\n\n' + theirs
        if decision == 'access-model':
            return '\t\t&UserAccessToken{},\n'
        if decision == 'user-delete':
            return ours.rsplit('func DeleteUserById', 1)[0] + theirs
        if decision == 'models':
            entries = re.findall(r'&model\.[A-Za-z]+\{\}', ours + theirs)
            return '\trequire.NoError(t, db.AutoMigrate(' + ', '.join(dict.fromkeys(entries)) + '))\n'
        raise RuntimeError(decision)

    p.write_text(PATTERN.sub(resolve, s))

# Keep the upgraded ORM, dialects, Go and network/Redis dependencies as a set.
s = source(BASE, 'go.mod')
s, count = re.subn(r'github.com/grafana/sobek [^\n]+', 'github.com/Calcium-Ion/moejs v0.1.0-alpha.3', s)
if count != 1:
    raise RuntimeError('Sobek dependency not found exactly once')
(ROOT / 'go.mod').write_text(s)
sums = set(source(BASE, 'go.sum').splitlines()) | set(source(UPSTREAM, 'go.sum').splitlines())
(ROOT / 'go.sum').write_text('\n'.join(sorted(sums)) + '\n')

# Retain the extension composition boundary and initialize the legacy deadline
# only after core migration, never while constructing the model list.
replace_once(
    'model/main.go',
    '\tif err := InitializeUserAuthVersions(); err != nil {\n\t\treturn err\n\t}\n',
    '\tif err := InitializeUserAuthVersions(); err != nil {\n\t\treturn err\n\t}\n'
    '\tif err := EnsureLegacyAccessTokenRetireAt(common.GetTimestamp()); err != nil {\n'
    '\t\treturn fmt.Errorf("initialize legacy access token deadline: %w", err)\n\t}\n',
)

# Preserve custom keys and new upstream labels across adjacent sorted-key
# conflicts. Parsing and re-emission remove duplicate equal translation keys.
for path in (ROOT / 'web/src/i18n/locales').glob('*.json'):
    s = path.read_text()
    if '<<<<<<<' not in s:
        continue
    s = PATTERN.sub(lambda m: m.group(1) + m.group(2), s)
    data = json.loads(s)
    path.write_text(json.dumps(data, ensure_ascii=False, indent=2) + '\n')

# The wallet has a three-account ledger, not the removed legacy top-up dialog.
# Do not restore the duplicate wallet; keep the applicable upstream precision
# and translation regression on the real recharge component.
if not (ROOT / 'web/src/extensions/chuanapi/wallet/components/ledger-table.tsx').exists():
    raise RuntimeError('custom wallet ledger not found')
(ROOT / 'web/src/features/wallet/components/dialogs/billing-history-dialog.tsx').unlink()
p = ROOT / 'web/src/features/wallet/__tests__/copy.test.tsx'
s = p.read_text()
s = s.replace("import { BillingHistoryDialog } from '@/features/wallet/components/dialogs/billing-history-dialog'\n", '')
s = s.replace("import { api } from '@/lib/api'\n", '')
s = s.replace("import { RechargeFormCard } from '../components/recharge-form-card'", "import { RechargeFormCard } from '@/extensions/chuanapi/wallet/components/recharge-form-card'")
a = s.index("it('translates billing history statuses")
b = s.index('it.each([', a)
p.write_text(s[:a] + s[b:])
replace_once('model/migration_registry_test.go', '\t\t&AuthzRole{},\n', '\t\t&AuthzRole{},\n\t\t&UserAccessToken{},\n')
replace_once('model/migration_registry_test.go', 'require.Len(t, models, 35)', 'require.Len(t, models, 36)')
replace_once('model/migration_registry_test.go', 'assert.IsType(t, &AuthzRole{}, models[len(models)-1])', 'assert.IsType(t, &UserAccessToken{}, models[len(models)-1])')

# Upstream replaces the obsolete single-token component with scoped-token
# components and their create/edit/revoke/legacy transition regression suites.
(ROOT / 'web/src/features/security/components/__tests__/access-token-card.test.tsx').unlink()
for p in ROOT.rglob('*'):
    if not p.is_file() or '.git' in p.parts:
        continue
    try:
        s = p.read_text()
    except (UnicodeError, OSError):
        continue
    if re.search(r'^<<<<<<< |^>>>>>>> ', s, re.M):
        raise RuntimeError(f'unresolved conflict: {p}')
print('Reviewed conflicts resolved; upgraded database dependencies retained.')
