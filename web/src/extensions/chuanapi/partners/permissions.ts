import { hasPermission } from '@/lib/admin-permissions'
import { ROLE } from '@/lib/roles'
import type { AuthUser } from '@/stores/auth-store'

// Match the server's admin-role gate before checking the delegated grants.
// Updating settings requires a readable revision in this editor, and the
// review endpoint itself requires both read and review on applications.
export function partnerManagementAccess(user?: AuthUser | null) {
  const admin = (user?.role ?? ROLE.GUEST) >= ROLE.ADMIN
  const readApplications =
    admin && hasPermission(user, 'partner_applications', 'read')
  const readSettings = admin && hasPermission(user, 'partner_settings', 'read')
  return {
    open: readApplications || readSettings,
    readApplications,
    readSettings,
    reviewApplications:
      readApplications && hasPermission(user, 'partner_applications', 'review'),
    editSettings:
      readSettings && hasPermission(user, 'partner_settings', 'update'),
  }
}
