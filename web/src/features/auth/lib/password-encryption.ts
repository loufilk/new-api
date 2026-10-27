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
import { t } from 'i18next'

import { api } from '@/lib/api'

interface PasswordEncryptionKey {
  kid: string
  public_key: string
}

export interface EncryptedPassword {
  password_encrypted: string
  encryption_key_id: string
}

const KEY_CACHE_TTL_MS = 5 * 60_000

let cachedKey: PasswordEncryptionKey | null = null
let cachedAt = 0

export function clearPasswordEncryptionCache(): void {
  cachedKey = null
  cachedAt = 0
}

export async function encryptPassword(
  password: string
): Promise<EncryptedPassword> {
  try {
    const key = await getPasswordEncryptionKey()
    const ciphertext = await rsaOaepEncrypt(password, key.public_key, key.kid)
    return {
      password_encrypted: ciphertext,
      encryption_key_id: key.kid,
    }
  } catch (error: unknown) {
    clearPasswordEncryptionCache()
    throw new Error(t('Login failed'), { cause: error })
  }
}

async function getPasswordEncryptionKey(): Promise<PasswordEncryptionKey> {
  const now = Date.now()
  if (cachedKey && now - cachedAt < KEY_CACHE_TTL_MS) {
    return cachedKey
  }

  const response = await api.get<{
    success: boolean
    data?: PasswordEncryptionKey
  }>('/api/user/login/encryption-key')
  const key = response.data?.data
  if (!response.data?.success || !key?.kid || !key.public_key) {
    throw new Error('Password encryption key is unavailable')
  }
  cachedKey = key
  cachedAt = now
  return key
}

async function rsaOaepEncrypt(
  password: string,
  publicKeyPEM: string,
  keyId: string
): Promise<string> {
  if (typeof globalThis.crypto?.subtle !== 'undefined') {
    try {
      const publicKey = await globalThis.crypto.subtle.importKey(
        'spki',
        pemToDER(publicKeyPEM),
        { name: 'RSA-OAEP', hash: 'SHA-256' },
        false,
        ['encrypt']
      )
      const plaintext = new TextEncoder().encode(password)
      const algorithm = publicKey.algorithm as RsaHashedKeyAlgorithm
      if (algorithm.modulusLength < 2048 || algorithm.modulusLength > 16384) {
        throw new Error('Password encryption key has an unsupported size')
      }
      if (plaintext.byteLength > algorithm.modulusLength / 8 - 66) {
        const secret = globalThis.crypto.getRandomValues(new Uint8Array(32))
        const nonce = globalThis.crypto.getRandomValues(new Uint8Array(12))
        const key = await globalThis.crypto.subtle.importKey(
          'raw',
          secret,
          'AES-GCM',
          false,
          ['encrypt']
        )
        const [wrappedKey, ciphertext] = await Promise.all([
          globalThis.crypto.subtle.encrypt(
            {
              name: 'RSA-OAEP',
              label: new TextEncoder().encode('password-v2'),
            },
            publicKey,
            secret
          ),
          globalThis.crypto.subtle.encrypt(
            {
              name: 'AES-GCM',
              iv: nonce,
              additionalData: new TextEncoder().encode(`password-v2:${keyId}`),
            },
            key,
            plaintext
          ),
        ])
        return [
          'v2',
          arrayBufferToBase64(wrappedKey),
          arrayBufferToBase64(nonce.buffer),
          arrayBufferToBase64(ciphertext),
        ].join('.')
      }
      const ciphertext = await globalThis.crypto.subtle.encrypt(
        { name: 'RSA-OAEP' },
        publicKey,
        plaintext
      )
      return arrayBufferToBase64(ciphertext)
    } catch {
      // Older implementations may expose SubtleCrypto without supporting the
      // required RSA-OAEP parameters; the HTTP-compatible fallback handles it.
    }
  }

  // Only SubtleCrypto requires a secure context. Keep the browser fallback
  // lazy, and require the browser CSPRNG for every OAEP seed, AES key and nonce.
  if (typeof globalThis.crypto?.getRandomValues !== 'function') {
    throw new Error('Secure randomness is unavailable')
  }
  const [{ OAEP, mgf1 }, { sha256 }, publicKey] = await Promise.all([
    import('micro-rsa-dsa-dh/rsa.js'),
    import('@noble/hashes/sha2.js'),
    parseRsaPublicKey(publicKeyPEM),
  ])
  const plaintext = new TextEncoder().encode(password)
  if (plaintext.length > Math.ceil(publicKey.n.toString(2).length / 8) - 66) {
    const { gcm } = await import('@noble/ciphers/aes.js')
    const secret = globalThis.crypto.getRandomValues(new Uint8Array(32))
    const nonce = globalThis.crypto.getRandomValues(new Uint8Array(12))
    const wrappedKey = OAEP(
      sha256,
      mgf1(sha256),
      new TextEncoder().encode('password-v2')
    ).encrypt(publicKey, secret)
    const ciphertext = gcm(
      secret,
      nonce,
      new TextEncoder().encode(`password-v2:${keyId}`)
    ).encrypt(plaintext)
    return [
      'v2',
      arrayBufferToBase64(wrappedKey.buffer),
      arrayBufferToBase64(nonce.buffer),
      arrayBufferToBase64(ciphertext.buffer),
    ].join('.')
  }
  const ciphertext = OAEP(sha256, mgf1(sha256)).encrypt(publicKey, plaintext)
  return arrayBufferToBase64(ciphertext.buffer)
}

async function parseRsaPublicKey(
  pem: string
): Promise<{ n: bigint; e: bigint }> {
  const { BitString, Integer, Null, ObjectIdentifier, Sequence, fromBER } =
    await import('asn1js')
  const der = pemToDER(pem)
  const limits = { maxDepth: 5, maxNodes: 10, maxContentLength: 4096 }
  const spki = fromBER(der, limits)
  if (
    spki.offset !== der.byteLength ||
    !(spki.result instanceof Sequence) ||
    spki.result.lenBlock.isIndefiniteForm ||
    spki.result.valueBlock.value.length !== 2
  ) {
    throw new Error('Password encryption key is not valid SPKI')
  }
  const [algorithm, encodedKey] = spki.result.valueBlock.value
  if (
    !(algorithm instanceof Sequence) ||
    algorithm.lenBlock.isIndefiniteForm ||
    algorithm.valueBlock.value.length !== 2 ||
    !(algorithm.valueBlock.value[0] instanceof ObjectIdentifier) ||
    algorithm.valueBlock.value[0].valueBlock.toString() !==
      '1.2.840.113549.1.1.1' ||
    !(algorithm.valueBlock.value[1] instanceof Null) ||
    algorithm.valueBlock.value[1].lenBlock.length !== 0 ||
    !(encodedKey instanceof BitString) ||
    encodedKey.idBlock.isConstructed ||
    encodedKey.valueBlock.unusedBits !== 0
  ) {
    throw new Error('Password encryption key is not RSA')
  }
  const keyBytes = encodedKey.valueBlock.valueHexView
  const rsa = fromBER(keyBytes, limits)
  if (
    rsa.offset !== keyBytes.byteLength ||
    !(rsa.result instanceof Sequence) ||
    rsa.result.lenBlock.isIndefiniteForm ||
    rsa.result.valueBlock.value.length !== 2
  ) {
    throw new Error('Password encryption RSA key is invalid')
  }
  const [modulus, exponent] = rsa.result.valueBlock.value
  if (
    !(modulus instanceof Integer) ||
    !(exponent instanceof Integer) ||
    modulus.valueBlock.valueHexView.length > 2049 ||
    exponent.valueBlock.valueHexView.length > 33
  ) {
    throw new Error('Password encryption RSA parameters are invalid')
  }
  const n = modulus.toBigInt()
  const e = exponent.toBigInt()
  const bits = n.toString(2).length
  if (
    n <= 0n ||
    bits < 2048 ||
    bits > 16384 ||
    n % 2n === 0n ||
    e < 3n ||
    e % 2n === 0n ||
    e >= 2n ** 256n
  ) {
    throw new Error('Password encryption RSA parameters are unsupported')
  }
  return { n, e }
}

function pemToDER(pem: string): ArrayBuffer {
  if (
    pem.length > 8192 ||
    !/^-----BEGIN PUBLIC KEY-----\s+[A-Za-z0-9+/=\s]+-----END PUBLIC KEY-----\s*$/.test(
      pem
    )
  ) {
    throw new Error('Password encryption key is not valid PEM')
  }
  const body = pem
    .replace('-----BEGIN PUBLIC KEY-----', '')
    .replace('-----END PUBLIC KEY-----', '')
    .replaceAll(/\s+/g, '')
  const binary = atob(body)
  const bytes = new Uint8Array(binary.length)
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index)
  }
  return bytes.buffer
}

function arrayBufferToBase64(buffer: ArrayBuffer): string {
  const bytes = new Uint8Array(buffer)
  let binary = ''
  for (const byte of bytes) {
    binary += String.fromCharCode(byte)
  }
  return btoa(binary)
}
