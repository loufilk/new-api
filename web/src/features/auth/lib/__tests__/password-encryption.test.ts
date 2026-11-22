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
import {
  createDecipheriv,
  generateKeyPairSync,
  privateDecrypt,
  webcrypto,
} from 'node:crypto'

import {
  BitString,
  Integer,
  Null,
  ObjectIdentifier,
  Sequence,
  fromBER,
} from 'asn1js'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import {
  clearPasswordEncryptionCache,
  encryptPassword,
} from '../password-encryption'

beforeEach(() => {
  clearPasswordEncryptionCache()
  vi.stubGlobal('crypto', {
    getRandomValues: webcrypto.getRandomValues.bind(webcrypto),
  })
})

afterEach(() => {
  clearPasswordEncryptionCache()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

function serveEncryptionKey(modulusLength = 2048) {
  const keys = generateKeyPairSync('rsa', { modulusLength })
  const response = {
    data: {
      success: true,
      data: {
        kid: 'password-test-key',
        public_key: keys.publicKey
          .export({ type: 'spki', format: 'pem' })
          .toString(),
      },
    },
  }
  const request = vi.spyOn(api, 'get').mockResolvedValue(response)
  return { ...keys, response, request }
}

it('rejects password encryption when secure random bytes are unavailable', async () => {
  serveEncryptionKey()
  vi.stubGlobal('crypto', undefined)

  await expect(encryptPassword('password-must-stay-private')).rejects.toThrow(
    'Login failed'
  )
})

it('rejects an undersized RSA public key in the browser fallback', async () => {
  serveEncryptionKey(1024)

  await expect(encryptPassword('password-must-stay-private')).rejects.toThrow(
    'Login failed'
  )
})

it.each([
  {
    name: 'fallback last direct-OAEP byte',
    password: 'x'.repeat(190),
    envelope: false,
    native: false,
  },
  {
    name: 'fallback first hybrid byte',
    password: 'x'.repeat(191),
    envelope: true,
    native: false,
  },
  {
    name: 'Web Crypto last direct-OAEP byte',
    password: 'x'.repeat(190),
    envelope: false,
    native: true,
  },
  {
    name: 'Web Crypto first hybrid byte',
    password: 'x'.repeat(191),
    envelope: true,
    native: true,
  },
])(
  'preserves the server wire format at the $name boundary',
  async ({ password, envelope, native }) => {
    const { privateKey } = serveEncryptionKey()
    if (native) vi.stubGlobal('crypto', webcrypto)

    const encrypted = await encryptPassword(password)
    const parts = encrypted.password_encrypted.split('.')

    expect(encrypted.encryption_key_id).toBe('password-test-key')
    expect(parts[0] === 'v2').toBe(envelope)
    if (!envelope) {
      expect(
        privateDecrypt(
          { key: privateKey, oaepHash: 'sha256' },
          Buffer.from(parts[0], 'base64')
        ).toString()
      ).toBe(password)
      return
    }
    const secret = privateDecrypt(
      {
        key: privateKey,
        oaepHash: 'sha256',
        oaepLabel: Buffer.from('password-v2'),
      },
      Buffer.from(parts[1], 'base64')
    )
    expect(secret.length).toBe(32)
    const nonce = Buffer.from(parts[2], 'base64')
    expect(nonce.length).toBe(12)
    const ciphertext = Buffer.from(parts[3], 'base64')
    const decipher = createDecipheriv('aes-256-gcm', secret, nonce)
    decipher.setAAD(Buffer.from('password-v2:password-test-key'))
    decipher.setAuthTag(ciphertext.subarray(-16))
    expect(
      Buffer.concat([
        decipher.update(ciphertext.subarray(0, -16)),
        decipher.final(),
      ]).toString()
    ).toBe(password)
  }
)

it('uses the compatible fallback when a browser exposes unsupported Web Crypto', async () => {
  const { privateKey } = serveEncryptionKey()
  vi.stubGlobal('crypto', {
    getRandomValues: webcrypto.getRandomValues.bind(webcrypto),
    subtle: {
      importKey: async () => {
        throw new Error('NotSupportedError')
      },
    },
  })

  const encrypted = await encryptPassword('Unicode 密码 🔒')

  expect(
    privateDecrypt(
      { key: privateKey, oaepHash: 'sha256' },
      Buffer.from(encrypted.password_encrypted, 'base64')
    ).toString('utf8')
  ).toBe('Unicode 密码 🔒')
})

it.each(['key id', 'ciphertext', 'nonce'] as const)(
  'produces a hybrid envelope that rejects a changed %s',
  async (changed) => {
    const { privateKey } = serveEncryptionKey()
    const encrypted = await encryptPassword('🔒'.repeat(128))
    const parts = encrypted.password_encrypted.split('.')
    const secret = privateDecrypt(
      {
        key: privateKey,
        oaepHash: 'sha256',
        oaepLabel: Buffer.from('password-v2'),
      },
      Buffer.from(parts[1], 'base64')
    )
    const nonce = Buffer.from(parts[2], 'base64')
    const ciphertext = Buffer.from(parts[3], 'base64')
    if (changed === 'ciphertext') ciphertext[0] ^= 1
    if (changed === 'nonce') nonce[0] ^= 1
    const decipher = createDecipheriv('aes-256-gcm', secret, nonce)
    decipher.setAAD(
      Buffer.from(
        `password-v2:${changed === 'key id' ? 'different-key' : encrypted.encryption_key_id}`
      )
    )
    decipher.setAuthTag(ciphertext.subarray(-16))
    decipher.update(ciphertext.subarray(0, -16))

    expect(() => decipher.final()).toThrow()
  }
)

it('binds a hybrid envelope wrapped key to the existing OAEP label', async () => {
  const { privateKey } = serveEncryptionKey()
  const encrypted = await encryptPassword('🔒'.repeat(128))
  const wrappedKey = Buffer.from(
    encrypted.password_encrypted.split('.')[1],
    'base64'
  )

  expect(() =>
    privateDecrypt({ key: privateKey, oaepHash: 'sha256' }, wrappedKey)
  ).toThrow()
})

it('clears an invalid cached key and retries with a fresh key without returning plaintext', async () => {
  const { privateKey, request, response } = serveEncryptionKey()
  request.mockResolvedValueOnce({
    data: {
      success: true,
      data: { kid: 'bad-key', public_key: 'invalid-public-key' },
    },
  })

  await expect(encryptPassword('private-password')).rejects.toThrow(
    'Login failed'
  )
  request.mockResolvedValue(response)
  const encrypted = await encryptPassword('private-password')

  expect(request).toHaveBeenCalledTimes(2)
  expect(
    privateDecrypt(
      { key: privateKey, oaepHash: 'sha256' },
      Buffer.from(encrypted.password_encrypted, 'base64')
    ).toString()
  ).toBe('private-password')
})

it.each([
  'algorithm',
  'parameters',
  'extra algorithm field',
  'extra key field',
  'trailing data',
  'unused bits',
] as const)('rejects an RSA SPKI with invalid %s', async (invalid) => {
  const { publicKey, response, request } = serveEncryptionKey()
  const parsed = fromBER(publicKey.export({ type: 'spki', format: 'der' }))
  expect(parsed.result).toBeInstanceOf(Sequence)
  const spki = parsed.result as Sequence
  const algorithm = spki.valueBlock.value[0] as Sequence
  const encodedKey = spki.valueBlock.value[1] as BitString
  switch (invalid) {
    case 'algorithm':
      algorithm.valueBlock.value[0] = new ObjectIdentifier({
        value: '1.2.840.10045.2.1',
      })
      break
    case 'parameters':
      algorithm.valueBlock.value[1] = new Integer({ value: 1 })
      break
    case 'extra algorithm field':
      algorithm.valueBlock.value.push(new Null())
      break
    case 'extra key field': {
      const key = fromBER(encodedKey.valueBlock.valueHexView).result as Sequence
      key.valueBlock.value.push(new Integer({ value: 1 }))
      spki.valueBlock.value[1] = new BitString({ valueHex: key.toBER(false) })
      break
    }
    case 'unused bits':
      encodedKey.valueBlock.unusedBits = 1
      break
  }
  let der = Buffer.from(spki.toBER(false))
  if (invalid === 'trailing data') {
    der = Buffer.concat([der, Buffer.from([0, 0])])
  }
  response.data.data.public_key = `-----BEGIN PUBLIC KEY-----\n${der.toString('base64')}\n-----END PUBLIC KEY-----`
  request.mockResolvedValue(response)

  await expect(encryptPassword('private-password')).rejects.toThrow(
    'Login failed'
  )
})
