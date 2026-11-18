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
import { create } from 'zustand'
import { persist } from 'zustand/middleware'

export const EMPTY_NOTIFICATION_KEYS: string[] = []

export function notificationScope(userId?: number): string {
  return userId && Number.isInteger(userId) && userId > 0
    ? `user:${userId}`
    : 'public'
}

interface NotificationState {
  // Last read Notice content signature (full trimmed message)
  lastReadNotice: string
  // Array of read announcement keys (id or content hash)
  readAnnouncementKeys: string[]
  lastReadNoticeByUser: Record<string, string>
  readAnnouncementKeysByUser: Record<string, string[]>
  // Timestamp of last "Close Today" action
  closedUntilDate: string | null
  // Shared across headers for this page session; deliberately not persisted.
  autoPopupShown: boolean

  // Actions
  markNoticeRead: (noticeContent: string, scope?: string) => void
  markAnnouncementsRead: (keys: string[], scope?: string) => void
  setClosedUntilDate: (date: string | null) => void
  setAutoPopupShown: () => void
  isAnnouncementRead: (key: string) => boolean
  isNoticeClosed: () => boolean
}

/**
 * Notification store for tracking read status of Notice and Announcements
 * Persists to localStorage to maintain state across sessions
 */
export const useNotificationStore = create<NotificationState>()(
  persist(
    (set, get) => ({
      lastReadNotice: '',
      readAnnouncementKeys: [],
      lastReadNoticeByUser: {},
      readAnnouncementKeysByUser: {},
      closedUntilDate: null,
      autoPopupShown: false,

      markNoticeRead: (noticeContent: string, scope = 'public') => {
        // Persist the full trimmed content so edits beyond 100 chars register
        const normalizedContent = noticeContent.trim()
        if (scope === 'public') {
          set({ lastReadNotice: normalizedContent })
          return
        }
        set((state) => ({
          lastReadNoticeByUser: {
            ...state.lastReadNoticeByUser,
            [scope]: normalizedContent,
          },
        }))
      },

      markAnnouncementsRead: (keys: string[], scope = 'public') => {
        if (scope !== 'public') {
          set((state) => ({
            readAnnouncementKeysByUser: {
              ...state.readAnnouncementKeysByUser,
              [scope]: [
                ...new Set([
                  ...(state.readAnnouncementKeysByUser[scope] ?? []),
                  ...keys,
                ]),
              ],
            },
          }))
          return
        }
        set((state) => ({
          readAnnouncementKeys: [
            ...new Set([...state.readAnnouncementKeys, ...keys]),
          ],
        }))
      },

      setClosedUntilDate: (date: string | null) => {
        set({ closedUntilDate: date })
      },
      setAutoPopupShown: () => {
        set({ autoPopupShown: true })
      },

      isAnnouncementRead: (key: string) => {
        return get().readAnnouncementKeys.includes(key)
      },

      isNoticeClosed: () => {
        const { closedUntilDate } = get()
        if (!closedUntilDate) return false

        const today = new Date().toDateString()
        return closedUntilDate === today
      },
    }),
    {
      name: 'notification-storage',
      partialize: (state) => ({
        lastReadNotice: state.lastReadNotice,
        readAnnouncementKeys: state.readAnnouncementKeys,
        lastReadNoticeByUser: state.lastReadNoticeByUser,
        readAnnouncementKeysByUser: state.readAnnouncementKeysByUser,
        closedUntilDate: state.closedUntilDate,
      }),
    }
  )
)
