/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, test } from 'vitest'

import {
  buildBaselineEntries,
  evaluateLintCi,
  hashContent,
  isSafeBaselinePath,
  normalizeDiagnosticPath,
  normalizeLineEndings,
  parseOxlintOutput,
  planBaselineWrite,
  sortFingerprints,
  toDiagnosticFingerprint,
} from '../lint-ci.mjs'

const scriptsDir = dirname(fileURLToPath(import.meta.url))
const baselinePath = join(scriptsDir, '..', 'lint-baseline.json')

const configSha256 = 'a'.repeat(64)
const baselineMeta = {
  version: 1,
  upstreamRevision: '7fd06381976cbea126d1f20973548bf60f20e37d',
  oxlintVersion: '1.74.0',
  configSha256,
}

const upstreamDiagnostic = {
  code: 'typescript(no-import-type-side-effects)',
  severity: 'error',
  line: 19,
  column: 1,
  message:
    'TypeScript will only remove the inline type specifiers which will leave behind a side effect import at runtime.',
}

function fingerprint(overrides = {}) {
  return { ...upstreamDiagnostic, ...overrides }
}

function diagnosticsFor(entries) {
  return entries.flatMap(([path, prints]) =>
    prints.map((print) => ({ path, fingerprint: print }))
  )
}

function evaluate({
  baselineFiles,
  diagnostics = [],
  numberOfFiles = 100,
  exitCode = 0,
  baselineOverrides = {},
  configHash = configSha256,
  contents = {},
}) {
  return evaluateLintCi({
    baseline: { ...baselineMeta, ...baselineOverrides, files: baselineFiles },
    lintResult: { numberOfFiles, diagnostics },
    exitCode,
    configHash,
    readFile: (path) => (path in contents ? contents[path] : null),
  })
}

describe('evaluateLintCi', () => {
  test('allows diagnostics recorded for an unmodified pinned upstream file', () => {
    const content = 'import { type SVGProps } from react\n'
    const result = evaluate({
      baselineFiles: {
        'src/icon.tsx': {
          sha256: hashContent(content),
          diagnostics: [fingerprint()],
        },
      },
      diagnostics: diagnosticsFor([['src/icon.tsx', [fingerprint()]]]),
      exitCode: 1,
      contents: { 'src/icon.tsx': content },
    })
    expect(result.ok).toBe(true)
    expect(result.failures).toEqual([])
  })

  test('allows recorded upstream warnings when the content is unmodified', () => {
    const content = 'selectionOnDrag={true}\n'
    const warning = fingerprint({
      code: 'react(jsx-boolean-value)',
      severity: 'warning',
      line: 32,
      column: 3,
    })
    const result = evaluate({
      baselineFiles: {
        'src/canvas.tsx': {
          sha256: hashContent(content),
          diagnostics: [warning],
        },
      },
      diagnostics: diagnosticsFor([['src/canvas.tsx', [warning]]]),
      contents: { 'src/canvas.tsx': content },
    })
    expect(result.ok).toBe(true)
  })

  test('rejects a diagnostic when the pinned file content changed', () => {
    const result = evaluate({
      baselineFiles: {
        'src/icon.tsx': {
          sha256: hashContent('original\n'),
          diagnostics: [fingerprint()],
        },
      },
      diagnostics: diagnosticsFor([['src/icon.tsx', [fingerprint()]]]),
      contents: { 'src/icon.tsx': 'locally edited\n' },
    })
    expect(result.ok).toBe(false)
    expect(result.failures.join('\n')).toContain(
      'modified file must lint clean'
    )
  })

  test('rejects diagnostics for a path that is not in the baseline', () => {
    const result = evaluate({
      baselineFiles: {},
      diagnostics: diagnosticsFor([
        ['src/features/local/file.tsx', [fingerprint()]],
      ]),
      contents: {},
    })
    expect(result.ok).toBe(false)
    expect(result.failures.join('\n')).toContain('unexpected lint diagnostic')
  })

  test('rejects local warnings outside the baseline', () => {
    const result = evaluate({
      baselineFiles: {},
      diagnostics: diagnosticsFor([
        ['src/features/local/file.tsx', [fingerprint({ severity: 'warning' })]],
      ]),
    })
    expect(result.ok).toBe(false)
    expect(result.failures.join('\n')).toContain('unexpected lint diagnostic')
  })

  test('rejects a changed fingerprint for a pinned file', () => {
    const content = 'import { type SVGProps } from react\n'
    const result = evaluate({
      baselineFiles: {
        'src/icon.tsx': {
          sha256: hashContent(content),
          diagnostics: [fingerprint()],
        },
      },
      diagnostics: diagnosticsFor([
        ['src/icon.tsx', [fingerprint({ line: 20 })]],
      ]),
      contents: { 'src/icon.tsx': content },
    })
    expect(result.ok).toBe(false)
    expect(result.failures.join('\n')).toContain('baseline diagnostics changed')
  })

  test('rejects a recorded diagnostic that is no longer reported', () => {
    const content = 'import { type SVGProps } from react\n'
    const result = evaluate({
      baselineFiles: {
        'src/icon.tsx': {
          sha256: hashContent(content),
          diagnostics: [fingerprint()],
        },
      },
      diagnostics: [],
      contents: { 'src/icon.tsx': content },
    })
    expect(result.ok).toBe(false)
    expect(result.failures.join('\n')).toContain(
      'recorded baseline diagnostics are no longer reported'
    )
  })

  test('rejects a missing baseline file entry', () => {
    const result = evaluate({
      baselineFiles: {
        'src/gone.tsx': { sha256: hashContent('x\n'), diagnostics: [] },
      },
      contents: {},
    })
    expect(result.ok).toBe(false)
    expect(result.failures.join('\n')).toContain('baseline file is missing')
  })

  test('rejects a changed oxlint config hash', () => {
    const result = evaluate({
      baselineFiles: {},
      configHash: 'b'.repeat(64),
    })
    expect(result.ok).toBe(false)
    expect(result.failures.join('\n')).toContain('oxlint config changed')
  })

  test('rejects an exit code that contradicts the reported diagnostics', () => {
    const inconsistentExit = evaluate({
      baselineFiles: {},
      exitCode: 1,
    })
    expect(inconsistentExit.ok).toBe(false)
    expect(inconsistentExit.failures.join('\n')).toContain(
      'exited with code 1 but reported no error diagnostics'
    )

    const warningOnlyExit = evaluate({
      baselineFiles: {},
      diagnostics: diagnosticsFor([
        ['src/icon.tsx', [fingerprint({ severity: 'warning' })]],
      ]),
      exitCode: 1,
    })
    expect(warningOnlyExit.ok).toBe(false)
    expect(warningOnlyExit.failures.join('\n')).toContain(
      'exited with code 1 but reported no error diagnostics'
    )

    const hiddenError = evaluate({ baselineFiles: {}, exitCode: 0 })
    expect(hiddenError.ok).toBe(true)

    const errorWithoutFailureExit = evaluate({
      baselineFiles: {},
      diagnostics: diagnosticsFor([['src/icon.tsx', [fingerprint()]]]),
      exitCode: 0,
    })
    expect(errorWithoutFailureExit.ok).toBe(false)
    expect(errorWithoutFailureExit.failures.join('\n')).toContain(
      'exited with code 0 despite reporting error diagnostics'
    )
  })

  test.each([
    ['version', { version: 2 }],
    ['upstreamRevision', { upstreamRevision: 'record manually' }],
    ['oxlintVersion', { oxlintVersion: '' }],
    ['configSha256', { configSha256: 'not-a-sha256' }],
  ])('rejects invalid %s baseline metadata', (_label, baselineOverrides) => {
    const result = evaluate({ baselineFiles: {}, baselineOverrides })
    expect(result.ok).toBe(false)
  })

  test('rejects a malformed baseline file entry', () => {
    const result = evaluate({
      baselineFiles: {
        'src/icon.tsx': { sha256: 'short', diagnostics: [] },
      },
      contents: { 'src/icon.tsx': 'x\n' },
    })
    expect(result.ok).toBe(false)
    expect(result.failures.join('\n')).toContain('baseline entry is malformed')
  })

  test('rejects an unparseable lint run', () => {
    const result = evaluateLintCi({
      baseline: { files: {} },
      lintResult: null,
      exitCode: 0,
      configHash: 'config-sha',
      readFile: () => null,
    })
    expect(result.ok).toBe(false)
    expect(result.failures.join('\n')).toContain(
      'did not produce parseable JSON'
    )
  })

  test('rejects an unexpected oxlint exit code', () => {
    const result = evaluate({ baselineFiles: {}, exitCode: 2 })
    expect(result.ok).toBe(false)
    expect(result.failures.join('\n')).toContain('unexpected code 2')
  })

  test('rejects a run that checked zero files', () => {
    const result = evaluate({ baselineFiles: {}, numberOfFiles: 0 })
    expect(result.ok).toBe(false)
    expect(result.failures.join('\n')).toContain('zero checked files')
  })

  test('rejects a malformed baseline', () => {
    const result = evaluateLintCi({
      baseline: { files: [] },
      lintResult: { numberOfFiles: 10, diagnostics: [] },
      exitCode: 0,
      configHash: 'config-sha',
      readFile: () => null,
    })
    expect(result.ok).toBe(false)
    expect(result.failures.join('\n')).toContain(
      'baseline is missing or malformed'
    )
  })
})

describe('parseOxlintOutput', () => {
  test('normalizes absolute filenames to workspace-relative paths', () => {
    const payload = JSON.stringify({
      number_of_files: 7,
      diagnostics: [
        {
          code: 'typescript(no-import-type-side-effects)',
          severity: 'error',
          message: 'm',
          labels: [{ span: { line: 3, column: 2 } }],
          filename: 'C:\\work\\web\\src\\icon.tsx',
        },
      ],
    })
    const parsed = parseOxlintOutput(payload, 'C:\\work\\web')
    expect(parsed?.numberOfFiles).toBe(7)
    expect(parsed?.diagnostics[0]?.path).toBe('src/icon.tsx')
    expect(parsed?.diagnostics[0]?.fingerprint).toEqual({
      code: 'typescript(no-import-type-side-effects)',
      severity: 'error',
      line: 3,
      column: 2,
      message: 'm',
    })
  })

  test('returns null for invalid JSON and for payloads without a file count', () => {
    expect(parseOxlintOutput('not json')).toBeNull()
    expect(parseOxlintOutput(JSON.stringify({ diagnostics: [] }))).toBeNull()
    expect(parseOxlintOutput(JSON.stringify({ number_of_files: 1 }))).toBeNull()
  })
})

describe('hash helpers', () => {
  test('normalizes CRLF before hashing so Windows checkouts match CI', () => {
    expect(normalizeLineEndings('a\r\nb\r\n')).toBe('a\nb\n')
    expect(hashContent('a\r\nb\r\n')).toBe(hashContent('a\nb\n'))
  })

  test('sorts fingerprints deterministically by position and code', () => {
    const sorted = sortFingerprints([
      fingerprint({ line: 9, column: 2, code: 'b' }),
      fingerprint({ line: 9, column: 1, code: 'a' }),
      fingerprint({ line: 2, column: 5, code: 'c' }),
    ])
    expect(
      sorted.map((item) => `${item.line}:${item.column}:${item.code}`)
    ).toEqual(['2:5:c', '9:1:a', '9:2:b'])
  })

  test('keeps the diagnostic path relative for plain relative input', () => {
    expect(normalizeDiagnosticPath('src/icon.tsx')).toBe('src/icon.tsx')
    expect(normalizeDiagnosticPath('./src/icon.tsx')).toBe('src/icon.tsx')
  })

  test('extracts fingerprints without label spans safely', () => {
    expect(
      toDiagnosticFingerprint({ code: 'x', severity: 'error', message: 'm' })
    ).toEqual({
      code: 'x',
      severity: 'error',
      line: 0,
      column: 0,
      message: 'm',
    })
  })
})

describe('buildBaselineEntries', () => {
  test('refreshes hashes and diagnostics and skips missing files', () => {
    const result = buildBaselineEntries({
      previousFiles: { 'src/kept.tsx': { sha256: 'stale', diagnostics: [] } },
      adoptedPaths: ['src/new.tsx', 'src/missing.tsx'],
      lintResult: {
        numberOfFiles: 3,
        diagnostics: diagnosticsFor([
          ['src/kept.tsx', [fingerprint()]],
          ['src/new.tsx', [fingerprint({ line: 4 })]],
        ]),
      },
      readFile: (path) =>
        path === 'src/missing.tsx' ? null : `${path} content\n`,
    })
    expect(Object.keys(result.entries).sort()).toEqual([
      'src/kept.tsx',
      'src/new.tsx',
    ])
    expect(result.entries['src/kept.tsx'].sha256).toBe(
      hashContent('src/kept.tsx content\n')
    )
    expect(result.entries['src/new.tsx'].diagnostics).toEqual([
      fingerprint({ line: 4 }),
    ])
    expect(result.skipped).toEqual(['src/missing.tsx'])
  })
})

describe('isSafeBaselinePath', () => {
  test('accepts workspace-relative paths and rejects escapes', () => {
    expect(isSafeBaselinePath('src/icon.tsx')).toBe(true)
    expect(isSafeBaselinePath('src\\nested\\icon.tsx')).toBe(true)
    expect(isSafeBaselinePath('../secret.ts')).toBe(false)
    expect(isSafeBaselinePath('src/../../secret.ts')).toBe(false)
    expect(isSafeBaselinePath('/absolute.ts')).toBe(false)
    expect(isSafeBaselinePath('C:/absolute.ts')).toBe(false)
    expect(isSafeBaselinePath('')).toBe(false)
    expect(isSafeBaselinePath('src//icon.tsx')).toBe(false)
  })
})

describe('planBaselineWrite', () => {
  const revision = '7fd06381976cbea126d1f20973548bf60f20e37d'
  const upstreamContent = 'original upstream\n'
  const previousFiles = {
    'src/icon.tsx': {
      sha256: hashContent(upstreamContent),
      diagnostics: [fingerprint()],
    },
  }
  const lintResult = {
    numberOfFiles: 5,
    diagnostics: diagnosticsFor([['src/icon.tsx', [fingerprint()]]]),
  }

  test('refuses to refresh a baseline for a locally modified file', () => {
    const plan = planBaselineWrite({
      previousFiles,
      lintResult,
      readFile: () => 'locally edited\n',
      upstreamRevision: revision,
      readUpstreamFile: () => ({ content: upstreamContent }),
    })
    expect(plan.ok).toBe(false)
    expect(plan.failures.join('\n')).toContain(
      'does not match upstream 7fd06381976cbea126d1f20973548bf60f20e37d'
    )
    expect(plan.entries).toEqual({})
  })

  test('requires a fixed upstream revision before planning a write', () => {
    const plan = planBaselineWrite({
      previousFiles,
      lintResult,
      readFile: () => upstreamContent,
      upstreamRevision: '',
      readUpstreamFile: () => ({ content: upstreamContent }),
    })
    expect(plan.ok).toBe(false)
    expect(plan.failures.join('\n')).toContain(
      'fixed upstream revision SHA is required'
    )
    expect(plan.entries).toEqual({})
  })

  test('rejects paths that escape the workspace', () => {
    const plan = planBaselineWrite({
      previousFiles: { '../outside.ts': previousFiles['src/icon.tsx'] },
      lintResult: { numberOfFiles: 1, diagnostics: [] },
      readFile: () => upstreamContent,
      upstreamRevision: revision,
      readUpstreamFile: () => ({ content: upstreamContent }),
    })
    expect(plan.ok).toBe(false)
    expect(plan.failures.join('\n')).toContain('unsafe baseline path')
    expect(plan.entries).toEqual({})
  })

  test('rejects a write when git cannot read the pinned revision', () => {
    const result = planBaselineWrite({
      previousFiles,
      lintResult,
      readFile: () => upstreamContent,
      upstreamRevision: revision,
      readUpstreamFile: () => ({ error: 'fatal: invalid object name' }),
    })
    expect(result.ok).toBe(false)
    expect(result.failures.join('\n')).toContain(
      'cannot read src/icon.tsx from upstream'
    )
    expect(result.entries).toEqual({})
  })

  test('rejects a write when the git reader throws', () => {
    const result = planBaselineWrite({
      previousFiles,
      lintResult,
      readFile: () => upstreamContent,
      upstreamRevision: revision,
      readUpstreamFile: () => {
        throw new Error('spawn failed')
      },
    })
    expect(result.ok).toBe(false)
    expect(result.failures.join('\n')).toContain('spawn failed')
    expect(result.entries).toEqual({})
  })

  test('rejects a write when oxlint checked zero files', () => {
    const result = planBaselineWrite({
      previousFiles,
      lintResult: { numberOfFiles: 0, diagnostics: [] },
      readFile: () => upstreamContent,
      upstreamRevision: revision,
      readUpstreamFile: () => ({ content: upstreamContent }),
    })
    expect(result.ok).toBe(false)
    expect(result.failures.join('\n')).toContain('zero checked files')
    expect(result.entries).toEqual({})
  })

  test('accepts verified upstream files and adopted files with matching content', () => {
    const contents = {
      'src/icon.tsx': upstreamContent,
      'src/new.tsx': 'new upstream file\n',
    }
    const plan = planBaselineWrite({
      previousFiles,
      adoptedPaths: ['src/new.tsx'],
      lintResult: {
        numberOfFiles: 5,
        diagnostics: diagnosticsFor([
          ['src/icon.tsx', [fingerprint()]],
          ['src/new.tsx', [fingerprint({ line: 4 })]],
        ]),
      },
      readFile: (path) => contents[path] ?? null,
      upstreamRevision: revision,
      readUpstreamFile: (_revision, path) => ({ content: contents[path] }),
    })
    expect(plan.ok).toBe(true)
    expect(plan.failures).toEqual([])
    expect(Object.keys(plan.entries).sort()).toEqual([
      'src/icon.tsx',
      'src/new.tsx',
    ])
    expect(plan.entries['src/icon.tsx'].sha256).toBe(
      hashContent(upstreamContent)
    )
    expect(plan.entries['src/new.tsx'].diagnostics).toEqual([
      fingerprint({ line: 4 }),
    ])
  })
})

describe('checked-in baseline', () => {
  const baseline = JSON.parse(readFileSync(baselinePath, 'utf8'))

  test('tracks the pinned upstream revision and every entry has a content hash', () => {
    expect(baseline.version).toBe(1)
    expect(baseline.upstreamRevision).toBe(
      'b0bf2580e2491869ee9fe861c12b86153c3fe5f4'
    )
    expect(baseline.oxlintVersion).toBe('1.74.0')
    expect(baseline.configSha256).toMatch(/^[0-9a-f]{64}$/)
    const entries = Object.entries(baseline.files)
    expect(entries.length).toBeGreaterThan(0)
    for (const [path, entry] of entries) {
      expect(isSafeBaselinePath(path)).toBe(true)
      expect(path.startsWith('src/')).toBe(true)
      expect(entry.sha256).toMatch(/^[0-9a-f]{64}$/)
      expect(Array.isArray(entry.diagnostics)).toBe(true)
    }
  })
})
