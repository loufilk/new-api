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
import { useAuthStore } from '@/stores/auth-store'

import { MESSAGE_STATUS, STORAGE_KEYS } from '../../constants'
import type { PlaygroundConfig, ParameterEnabled, Message } from '../../types'
import {
  finalizeMessage,
  isAssistantMessagePending,
  sanitizeMessagesOnLoad,
} from '../message/message-streaming-utils'
import { completeAssistantTiming } from '../message/message-timing-utils'
import { hasMessageContent } from '../message/message-utils'
import {
  MAX_LOADED_MESSAGE_CHARS,
  MAX_LOADED_MESSAGES_CHARS,
  MAX_STORED_MESSAGES,
  MAX_STORED_MESSAGES_BYTES,
  STORAGE_VERSION,
  messagesSchema,
  parameterEnabledSchema,
  playgroundConfigSchema,
} from './storage-schema'

type StoredEnvelope<T> = {
  version: number
  data: T
}

// Previous versions saved unbounded histories. Recover a practical localStorage
// payload rather than erasing it, while bounding how much JSON we parse.
const MAX_LEGACY_MESSAGES_BYTES = 8 * MAX_STORED_MESSAGES_BYTES
const utf8 = new TextEncoder()
const EMPTY_MESSAGES_ENVELOPE_BYTES = JSON.stringify({
  version: STORAGE_VERSION,
  data: [],
}).length

const TRUNCATED_CONTENT_SUFFIX = '\n\n[...]'

function getUserStorageKey(key: string): string | null {
  // Legacy data has no owner; never assign it to the next signed-in account.
  for (const legacyKey of Object.values(STORAGE_KEYS)) {
    localStorage.removeItem(legacyKey)
  }
  const userId = useAuthStore.getState().auth.user?.id
  return userId == null ? null : `${key}:${userId}`
}

function readStoredValue(baseKey: string): unknown | null {
  const key = getUserStorageKey(baseKey)
  if (!key) return null
  const saved = localStorage.getItem(key)
  if (!saved) return null

  return JSON.parse(saved) as unknown
}

function readStoredMessagesValue(): {
  value: unknown
  oversized: boolean
} | null {
  const key = getUserStorageKey(STORAGE_KEYS.MESSAGES)
  if (!key) return null
  const saved = localStorage.getItem(key)
  if (!saved) return null

  if (saved.length > MAX_LEGACY_MESSAGES_BYTES) return null

  return {
    value: JSON.parse(saved) as unknown,
    oversized: utf8.encode(saved).length > MAX_STORED_MESSAGES_BYTES,
  }
}

function unwrapStoredValue(value: unknown): unknown {
  if (!value || typeof value !== 'object') {
    return value
  }

  if ('version' in value && 'data' in value) {
    return (value as StoredEnvelope<unknown>).data
  }

  return value
}

function writeStoredValue<T>(baseKey: string, data: T): void {
  const key = getUserStorageKey(baseKey)
  if (!key) return
  const payload: StoredEnvelope<T> = {
    version: STORAGE_VERSION,
    data,
  }

  localStorage.setItem(key, JSON.stringify(payload))
}

function trimMessages(messages: Message[]): Message[] {
  if (messages.length <= MAX_STORED_MESSAGES) {
    return messages
  }

  return messages.slice(-MAX_STORED_MESSAGES)
}

function getMessageSize(message: Message): number {
  const versionsSize = message.versions.reduce(
    (total, version) => total + version.content.length,
    0
  )
  const reasoningSize = message.reasoning?.content.length ?? 0

  return versionsSize + reasoningSize
}

function truncateText(text: string, maxLength: number): string {
  if (text.length <= maxLength) {
    return text
  }

  if (maxLength <= TRUNCATED_CONTENT_SUFFIX.length) {
    return text.slice(0, maxLength)
  }

  return `${text.slice(0, maxLength - TRUNCATED_CONTENT_SUFFIX.length)}${TRUNCATED_CONTENT_SUFFIX}`
}

function boundStoredMessageText(message: Message): Message {
  let changed = false
  const versions = message.versions.map((version) => {
    // Repeated headings (including in user prompts) are not proof of a snapshot.
    const content = truncateText(version.content, MAX_LOADED_MESSAGE_CHARS)

    if (content === version.content) {
      return version
    }

    changed = true
    return {
      ...version,
      content,
    }
  })

  const reasoning = message.reasoning
    ? {
        ...message.reasoning,
        content: truncateText(
          message.reasoning.content,
          MAX_LOADED_MESSAGE_CHARS
        ),
      }
    : undefined

  if (reasoning?.content !== message.reasoning?.content) {
    changed = true
  }

  return changed ? { ...message, versions, reasoning } : message
}

function normalizeStoredMessageForLoad(message: Message): Message {
  const normalized = boundStoredMessageText(message)
  if (!isAssistantMessagePending(normalized)) {
    return normalized
  }

  const hasContent = hasMessageContent(normalized)
  const hasReasoning = normalized.reasoning?.content.trim()

  if (!hasContent && !hasReasoning) {
    return normalized
  }

  const completedAt =
    normalized.completedAt ??
    normalized.reasoning?.completedAt ??
    normalized.startedAt ??
    normalized.createdAt ??
    Date.now()

  return completeAssistantTiming(
    {
      ...finalizeMessage(normalized),
      status: MESSAGE_STATUS.COMPLETE,
      isReasoningStreaming: false,
    },
    completedAt
  )
}

function trimMessagesByContentSize(messages: Message[]): Message[] {
  let totalSize = 0
  const result: Message[] = []

  for (let index = messages.length - 1; index >= 0; index--) {
    const message = messages[index]
    const messageSize = getMessageSize(message)

    if (
      result.length > 0 &&
      totalSize + messageSize > MAX_LOADED_MESSAGES_CHARS
    ) {
      break
    }

    totalSize += messageSize
    result.push(message)
  }

  return result.reverse()
}

/**
 * Load playground config from localStorage
 */
export function loadConfig(): Partial<PlaygroundConfig> {
  try {
    const saved = readStoredValue(STORAGE_KEYS.CONFIG)
    if (!saved) return {}

    return playgroundConfigSchema.parse(unwrapStoredValue(saved))
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to load config:', error)
  }
  return {}
}

/**
 * Save playground config to localStorage
 */
export function saveConfig(config: Partial<PlaygroundConfig>): void {
  try {
    const parsed = playgroundConfigSchema.parse(config)
    writeStoredValue(STORAGE_KEYS.CONFIG, parsed)
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to save config:', error)
  }
}

/**
 * Load parameter enabled state from localStorage
 */
export function loadParameterEnabled(): Partial<ParameterEnabled> {
  try {
    const saved = readStoredValue(STORAGE_KEYS.PARAMETER_ENABLED)
    if (!saved) return {}

    return parameterEnabledSchema.parse(unwrapStoredValue(saved))
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to load parameter enabled:', error)
  }
  return {}
}

/**
 * Save parameter enabled state to localStorage
 */
export function saveParameterEnabled(
  parameterEnabled: Partial<ParameterEnabled>
): void {
  try {
    const parsed = parameterEnabledSchema.parse(parameterEnabled)
    writeStoredValue(STORAGE_KEYS.PARAMETER_ENABLED, parsed)
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to save parameter enabled:', error)
  }
}

/**
 * Load messages from localStorage
 */
export function loadMessages(): Message[] | null {
  try {
    const saved = readStoredMessagesValue()
    if (!saved) return null

    const parsed = messagesSchema.parse(
      unwrapStoredValue(saved.value)
    ) as Message[]
    const normalized = parsed.map(normalizeStoredMessageForLoad)
    const normalizedChanged = normalized.some(
      (message, index) => message !== parsed[index]
    )
    const trimmed = trimMessages(normalized)
    const sizeTrimmed = trimMessagesByContentSize(trimmed)
    const sanitized = sanitizeMessagesOnLoad(sizeTrimmed)

    if (
      saved.oversized ||
      normalizedChanged ||
      trimmed !== normalized ||
      sizeTrimmed !== trimmed ||
      sanitized !== sizeTrimmed
    ) {
      saveMessages(sanitized)
    }

    return sanitized
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to load messages:', error)
  }
  return null
}

/**
 * Save messages to localStorage
 */
export function saveMessages(messages: Message[]): void {
  try {
    const parsed = messagesSchema.parse(trimMessages(messages)) as Message[]
    const compacted = trimMessagesByContentSize(
      parsed.map(boundStoredMessageText)
    )
    const stored: Message[] = []
    let bytes = EMPTY_MESSAGES_ENVELOPE_BYTES
    for (let index = compacted.length - 1; index >= 0; index--) {
      const message = compacted[index]
      const size = utf8.encode(JSON.stringify(message)).length
      const overhead = stored.length ? 1 : 0
      if (bytes + overhead + size > MAX_STORED_MESSAGES_BYTES) {
        if (stored.length) break
        continue
      }
      bytes += overhead + size
      stored.push(message)
    }
    writeStoredValue(STORAGE_KEYS.MESSAGES, stored.reverse())
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to save messages:', error)
  }
}

/**
 * Clear the signed-in user's playground data
 */
export function clearPlaygroundData(): void {
  try {
    for (const baseKey of Object.values(STORAGE_KEYS)) {
      const key = getUserStorageKey(baseKey)
      if (key) localStorage.removeItem(key)
    }
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to clear playground data:', error)
  }
}
