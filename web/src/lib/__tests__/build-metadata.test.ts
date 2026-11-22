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
import { mkdtempSync, rmSync } from 'node:fs'
import { createRequire } from 'node:module'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { loadEnv, rspack } from '@rsbuild/core'
import { afterEach, expect, it, vi } from 'vitest'

const requireBundle = createRequire(import.meta.url)

afterEach(() => {
  vi.unstubAllEnvs()
})

it.each([
  {
    condition: 'the release version is set',
    version: 'v1.2.3-custom',
    expected: 'rv.v1.2.3-custom.2k6e8r7p',
  },
  {
    condition: 'the release version is empty',
    version: '',
    expected: 'rv.0000.2k6e8r7p',
  },
])('compiles the expected build revision when $condition', async (fixture) => {
  vi.stubEnv('VITE_REACT_APP_VERSION', fixture.version)
  const env = loadEnv({ mode: 'production', prefixes: ['VITE_'] })
  const directory = mkdtempSync(join(tmpdir(), 'new-api-build-metadata-'))
  const bundlePath = join(directory, 'metadata.cjs')

  try {
    const compiler = rspack({
      mode: 'production',
      target: 'node',
      entry: join(
        dirname(fileURLToPath(import.meta.url)),
        '../build-metadata.ts'
      ),
      output: {
        path: directory,
        filename: 'metadata.cjs',
        library: { type: 'commonjs2' },
      },
      module: {
        rules: [
          {
            test: /\.ts$/,
            loader: 'builtin:swc-loader',
            options: { jsc: { parser: { syntax: 'typescript' } } },
          },
        ],
      },
      plugins: [new rspack.DefinePlugin(env.publicVars)],
    })
    await new Promise<void>((resolve, reject) => {
      compiler.run((error, stats) => {
        compiler.close((closeError) => {
          if (error || closeError) {
            reject(error || closeError)
          } else if (stats?.hasErrors()) {
            reject(new Error(stats.toString({ all: false, errors: true })))
          } else {
            resolve()
          }
        })
      })
    })

    const metadata = requireBundle(bundlePath) as {
      getBuildRevision: () => string
    }
    expect(metadata.getBuildRevision()).toBe(fixture.expected)
  } finally {
    delete requireBundle.cache[bundlePath]
    rmSync(directory, { recursive: true, force: true })
  }
})
