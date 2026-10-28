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
import { spawnSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { existsSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname, join, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const baselinePath = join(webRoot, 'scripts', 'lint-baseline.json')
const oxlintConfig = '.oxlintrc.json'
const oxlintTarget = '.'
const acceptedExitCodes = new Set([0, 1])
const supportedBaselineVersion = 1
const upstreamRevisionPattern = /^[0-9a-f]{7,40}$/i
const sha256Pattern = /^[0-9a-f]{64}$/i

export function normalizeLineEndings(text) {
  return text.replaceAll('\r\n', '\n')
}

export function hashContent(text) {
  return createHash('sha256')
    .update(normalizeLineEndings(text), 'utf8')
    .digest('hex')
}

export function normalizeDiagnosticPath(filename, rootDir = webRoot) {
  let value = String(filename).replaceAll('\\', '/')
  const normalizedRoot = rootDir.replaceAll('\\', '/').replace(/\/+$/, '')
  if (value.startsWith(`${normalizedRoot}/`)) {
    value = value.slice(normalizedRoot.length + 1)
  }
  while (value.startsWith('./')) {
    value = value.slice(2)
  }
  return value
}

export function isSafeBaselinePath(path) {
  if (typeof path !== 'string') {
    return false
  }
  const value = path.trim().replaceAll('\\', '/')
  if (value === '' || value.startsWith('/') || /^[a-zA-Z]:/.test(value)) {
    return false
  }
  return value
    .split('/')
    .every((segment) => segment !== '' && segment !== '.' && segment !== '..')
}

export function toDiagnosticFingerprint(diagnostic) {
  const span = diagnostic.labels?.[0]?.span
  return {
    code: diagnostic.code,
    severity: diagnostic.severity,
    line: Number(span?.line ?? 0),
    column: Number(span?.column ?? 0),
    message: diagnostic.message,
  }
}

export function sortFingerprints(items) {
  return [...items].sort(
    (a, b) =>
      a.line - b.line ||
      a.column - b.column ||
      (a.code ?? '').localeCompare(b.code ?? '') ||
      (a.message ?? '').localeCompare(b.message ?? '')
  )
}

function fingerprintsEqual(a, b) {
  return (
    a.code === b.code &&
    a.severity === b.severity &&
    a.line === b.line &&
    a.column === b.column &&
    a.message === b.message
  )
}

function sameFingerprintList(a, b) {
  if (a.length !== b.length) {
    return false
  }
  return a.every((item, index) => fingerprintsEqual(item, b[index]))
}

function describeFingerprints(fingerprints) {
  return fingerprints
    .map(
      (fingerprint) =>
        `${fingerprint.code} at ${fingerprint.line}:${fingerprint.column} (${fingerprint.severity})`
    )
    .join(', ')
}

export function parseOxlintOutput(stdout, rootDir = webRoot) {
  let payload
  try {
    payload = JSON.parse(stdout)
  } catch {
    return null
  }
  if (
    !payload ||
    !Array.isArray(payload.diagnostics) ||
    !Number.isInteger(payload.number_of_files)
  ) {
    return null
  }
  return {
    numberOfFiles: payload.number_of_files,
    diagnostics: payload.diagnostics.map((diagnostic) => ({
      path: normalizeDiagnosticPath(diagnostic.filename, rootDir),
      fingerprint: toDiagnosticFingerprint(diagnostic),
    })),
  }
}

export function evaluateLintCi({
  baseline,
  lintResult,
  exitCode,
  configHash,
  readFile,
}) {
  const failures = []

  if (!lintResult) {
    failures.push('oxlint did not produce parseable JSON output')
    return { ok: false, failures }
  }
  if (!acceptedExitCodes.has(exitCode)) {
    failures.push(`oxlint exited with unexpected code ${exitCode}`)
  }
  const hasErrorDiagnostics = lintResult.diagnostics.some(
    (item) => item.fingerprint.severity === 'error'
  )
  if (exitCode === 1 && !hasErrorDiagnostics) {
    failures.push('oxlint exited with code 1 but reported no error diagnostics')
  }
  if (exitCode === 0 && hasErrorDiagnostics) {
    failures.push(
      'oxlint exited with code 0 despite reporting error diagnostics'
    )
  }
  if (lintResult.numberOfFiles <= 0) {
    failures.push('oxlint reported zero checked files')
  }
  if (
    !baseline ||
    typeof baseline !== 'object' ||
    !baseline.files ||
    typeof baseline.files !== 'object' ||
    Array.isArray(baseline.files)
  ) {
    failures.push('lint baseline is missing or malformed')
    return { ok: false, failures }
  }
  if (baseline.version !== supportedBaselineVersion) {
    failures.push(
      `unsupported lint baseline version: ${String(baseline.version)}`
    )
  }
  if (
    typeof baseline.upstreamRevision !== 'string' ||
    !upstreamRevisionPattern.test(baseline.upstreamRevision.trim())
  ) {
    failures.push('lint baseline upstreamRevision is missing or invalid')
  }
  if (
    typeof baseline.oxlintVersion !== 'string' ||
    baseline.oxlintVersion.trim() === ''
  ) {
    failures.push('lint baseline oxlintVersion is missing or invalid')
  }
  if (
    typeof baseline.configSha256 !== 'string' ||
    !sha256Pattern.test(baseline.configSha256)
  ) {
    failures.push('lint baseline configSha256 is missing or invalid')
  } else if (!configHash) {
    failures.push('oxlint config hash is unavailable')
  } else if (baseline.configSha256 !== configHash) {
    failures.push(
      'oxlint config changed since the baseline was recorded; review and refresh the baseline'
    )
  }

  const officialFiles = new Map()
  const modifiedFiles = new Set()
  for (const [path, entry] of Object.entries(baseline.files)) {
    if (
      !entry ||
      typeof entry !== 'object' ||
      typeof entry.sha256 !== 'string' ||
      !sha256Pattern.test(entry.sha256) ||
      !Array.isArray(entry.diagnostics)
    ) {
      failures.push(`baseline entry is malformed: ${path}`)
      continue
    }
    const content = readFile(path)
    if (content == null) {
      failures.push(`baseline file is missing: ${path}`)
      continue
    }
    if (hashContent(content) === entry.sha256) {
      officialFiles.set(path, entry)
    } else {
      modifiedFiles.add(path)
    }
  }

  const observed = new Map()
  for (const item of lintResult.diagnostics) {
    const list = observed.get(item.path) ?? []
    list.push(item.fingerprint)
    observed.set(item.path, list)
  }

  for (const [path, fingerprints] of observed) {
    if (modifiedFiles.has(path)) {
      failures.push(
        `modified file must lint clean: ${path} (${describeFingerprints(fingerprints)})`
      )
      continue
    }
    const entry = officialFiles.get(path)
    if (!entry) {
      failures.push(
        `unexpected lint diagnostic: ${path} (${describeFingerprints(fingerprints)})`
      )
      continue
    }
    const expected = sortFingerprints(entry.diagnostics ?? [])
    const actual = sortFingerprints(fingerprints)
    if (!sameFingerprintList(expected, actual)) {
      failures.push(
        `baseline diagnostics changed for ${path}: expected [${describeFingerprints(expected)}] but observed [${describeFingerprints(actual)}]`
      )
    }
  }

  for (const [path, entry] of officialFiles) {
    if (
      !observed.has(path) &&
      Array.isArray(entry.diagnostics) &&
      entry.diagnostics.length > 0
    ) {
      failures.push(
        `recorded baseline diagnostics are no longer reported: ${path}`
      )
    }
  }

  return { ok: failures.length === 0, failures }
}

export function buildBaselineEntries({
  previousFiles = {},
  adoptedPaths = [],
  lintResult,
  readFile,
}) {
  const observed = new Map()
  for (const item of lintResult.diagnostics) {
    const list = observed.get(item.path) ?? []
    list.push(item.fingerprint)
    observed.set(item.path, list)
  }

  const entries = {}
  const skipped = []
  const paths = [...Object.keys(previousFiles), ...adoptedPaths].sort()
  for (const path of paths) {
    const content = readFile(path)
    if (content == null) {
      skipped.push(path)
      continue
    }
    entries[path] = {
      sha256: hashContent(content),
      diagnostics: sortFingerprints(observed.get(path) ?? []),
    }
  }
  return { entries, skipped }
}

export function planBaselineWrite({
  previousFiles = {},
  adoptedPaths = [],
  lintResult,
  readFile,
  upstreamRevision,
  readUpstreamFile,
}) {
  const revision = String(upstreamRevision ?? '').trim()
  if (!upstreamRevisionPattern.test(revision)) {
    return {
      ok: false,
      failures: [
        'a fixed upstream revision SHA is required before writing the baseline',
      ],
      entries: {},
    }
  }
  if (!lintResult || lintResult.numberOfFiles <= 0) {
    return {
      ok: false,
      failures: ['oxlint reported zero checked files; baseline not written'],
      entries: {},
    }
  }
  const failures = []
  const paths = [
    ...new Set([...Object.keys(previousFiles), ...adoptedPaths]),
  ].sort()
  for (const path of paths) {
    if (!isSafeBaselinePath(path)) {
      failures.push(`unsafe baseline path: ${path}`)
      continue
    }
    const content = readFile(path)
    if (content == null) {
      failures.push(`baseline file is missing: ${path}`)
      continue
    }
    let upstream
    try {
      upstream = readUpstreamFile(revision, path)
    } catch (error) {
      upstream = { error: error.message }
    }
    if (!upstream || upstream.error) {
      failures.push(
        `cannot read ${path} from upstream ${revision}: ${upstream?.error ?? 'file not found'}`
      )
      continue
    }
    if (typeof upstream.content !== 'string') {
      failures.push(`unexpected upstream content for ${path} at ${revision}`)
      continue
    }
    if (hashContent(upstream.content) !== hashContent(content)) {
      failures.push(
        `${path} is modified locally and does not match upstream ${revision}; refusing to refresh its baseline`
      )
    }
  }
  if (failures.length > 0) {
    return { ok: false, failures, entries: {} }
  }
  const { entries, skipped } = buildBaselineEntries({
    previousFiles,
    adoptedPaths,
    lintResult,
    readFile,
  })
  return { ok: true, failures: [], entries, skipped }
}

function readUpstreamFileFromGit(revision, path) {
  const result = spawnSync('git', ['show', `${revision}:web/${path}`], {
    cwd: webRoot,
    encoding: 'utf8',
    maxBuffer: 64 * 1024 * 1024,
    timeout: 30_000,
  })
  if (result.error) {
    return { error: result.error.message }
  }
  if (result.status !== 0) {
    return {
      error: (
        result.stderr || `git show exited with code ${result.status}`
      ).trim(),
    }
  }
  return { content: result.stdout }
}

function readInstalledOxlintVersion() {
  const manifestPath = join(webRoot, 'node_modules', 'oxlint', 'package.json')
  if (!existsSync(manifestPath)) {
    return null
  }
  try {
    return JSON.parse(readFileSync(manifestPath, 'utf8')).version ?? null
  } catch {
    return null
  }
}

function readConfigHash() {
  const configPath = join(webRoot, oxlintConfig)
  if (!existsSync(configPath)) {
    return null
  }
  return hashContent(readFileSync(configPath, 'utf8'))
}

function runOxlint() {
  const oxlintBin = join(webRoot, 'node_modules', 'oxlint', 'bin', 'oxlint')
  if (!existsSync(oxlintBin)) {
    return {
      status: null,
      error: `oxlint is not installed at ${relative(webRoot, oxlintBin)}; run bun install first`,
    }
  }
  const result = spawnSync(
    process.execPath,
    [oxlintBin, '-c', oxlintConfig, oxlintTarget, '--format', 'json'],
    { cwd: webRoot, encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 }
  )
  if (result.error) {
    return { status: null, error: result.error.message }
  }
  return { status: result.status, stdout: result.stdout, stderr: result.stderr }
}

function loadBaseline() {
  if (!existsSync(baselinePath)) {
    return null
  }
  try {
    return JSON.parse(readFileSync(baselinePath, 'utf8'))
  } catch {
    return null
  }
}

function parseArgs(argv) {
  const options = {
    writeBaseline: false,
    adoptPaths: [],
    upstreamRevision: null,
  }
  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index]
    if (arg === '--write-baseline') {
      options.writeBaseline = true
    } else if (arg === '--upstream-revision') {
      const value = argv[index + 1]
      if (!value) {
        throw new Error('--upstream-revision requires a commit SHA')
      }
      options.upstreamRevision = value
      index += 1
    } else if (arg === '--adopt') {
      const value = argv[index + 1]
      if (!value) {
        throw new Error('--adopt requires a file path')
      }
      options.adoptPaths.push(
        normalizeDiagnosticPath(value, webRoot).replace(/^web\//, '')
      )
      index += 1
    } else {
      throw new Error(`unknown argument: ${arg}`)
    }
  }
  return options
}

function readWorkspaceFile(path) {
  try {
    return readFileSync(join(webRoot, path), 'utf8')
  } catch {
    return null
  }
}

function printFailures(failures) {
  const limit = 50
  for (const failure of failures.slice(0, limit)) {
    console.error(`- ${failure}`)
  }
  if (failures.length > limit) {
    console.error(`- ... and ${failures.length - limit} more`)
  }
}

function writeBaseline(options) {
  const lint = runOxlint()
  if (lint.error) {
    console.error(`lint:ci: ${lint.error}`)
    process.exitCode = 2
    return
  }
  const lintResult = parseOxlintOutput(lint.stdout)
  if (!lintResult) {
    console.error(
      'lint:ci: oxlint output was not valid JSON; baseline not written'
    )
    process.exitCode = 2
    return
  }
  if (!acceptedExitCodes.has(lint.status)) {
    console.error(
      `lint:ci: oxlint exited with unexpected code ${lint.status}; baseline not written`
    )
    process.exitCode = 2
    return
  }
  const previous = loadBaseline() ?? { files: {} }
  const upstreamRevision =
    options.upstreamRevision ?? previous.upstreamRevision ?? ''
  const plan = planBaselineWrite({
    previousFiles: previous.files ?? {},
    adoptedPaths: options.adoptPaths,
    lintResult,
    readFile: readWorkspaceFile,
    upstreamRevision,
    readUpstreamFile: readUpstreamFileFromGit,
  })
  if (!plan.ok) {
    console.error('lint:ci: refusing to write baseline:')
    printFailures(plan.failures)
    process.exitCode = 2
    return
  }
  const baseline = {
    version: supportedBaselineVersion,
    upstreamRevision: String(upstreamRevision).trim(),
    oxlintVersion: readInstalledOxlintVersion(),
    configSha256: readConfigHash(),
    note: 'Curated allowlist of known upstream lint diagnostics. Each path must be byte-identical (LF-normalized sha256) to the pinned upstream revision; modified or new files must lint clean. Adopt new upstream files explicitly with --adopt after confirming they match the pinned revision.',
    files: plan.entries,
  }
  writeFileSync(baselinePath, `${JSON.stringify(baseline, null, 2)}\n`)
  console.log(
    `lint:ci baseline written with ${Object.keys(plan.entries).length} entries, oxlint ${baseline.oxlintVersion}`
  )
}

function checkBaseline() {
  const lint = runOxlint()
  if (lint.error) {
    console.error(`lint:ci: ${lint.error}`)
    process.exitCode = 2
    return
  }
  const lintResult = parseOxlintOutput(lint.stdout)
  const baseline = loadBaseline()
  if (!baseline) {
    console.error(
      'lint:ci: scripts/lint-baseline.json is missing or unreadable'
    )
    process.exitCode = 2
    return
  }
  const installedVersion = readInstalledOxlintVersion()
  if (
    installedVersion &&
    baseline.oxlintVersion &&
    installedVersion !== baseline.oxlintVersion
  ) {
    console.error(
      `lint:ci: oxlint ${installedVersion} does not match baseline version ${baseline.oxlintVersion}; review and refresh the baseline`
    )
    process.exitCode = 2
    return
  }
  const { ok, failures } = evaluateLintCi({
    baseline,
    lintResult,
    exitCode: lint.status,
    configHash: readConfigHash(),
    readFile: readWorkspaceFile,
  })
  const allowed = lintResult
    ? lintResult.diagnostics.filter((item) =>
        Object.hasOwn(baseline.files ?? {}, item.path)
      ).length
    : 0
  console.log(
    `lint:ci checked ${lintResult?.numberOfFiles ?? 0} files, ${
      lintResult?.diagnostics.length ?? 0
    } diagnostics (${allowed} allowed for pinned upstream files), oxlint ${installedVersion ?? 'unknown'}`
  )
  if (!ok) {
    console.error('lint:ci failed:')
    printFailures(failures)
    process.exitCode = 1
    return
  }
  console.log('lint:ci passed')
}

function main() {
  let options
  try {
    options = parseArgs(process.argv.slice(2))
  } catch (error) {
    console.error(`lint:ci: ${error.message}`)
    process.exitCode = 2
    return
  }
  if (options.writeBaseline) {
    writeBaseline(options)
    return
  }
  checkBaseline()
}

if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  main()
}
