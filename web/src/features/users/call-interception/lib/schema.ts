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
import { z } from 'zod'

export const CALL_INTERCEPTION_REPLY_MAX_BYTES = 16000

export const callInterceptionSchema = z
  .object({
    enabled: z.boolean(),
    reply: z
      .string()
      .refine(
        (reply) => !reply.includes('\u0000'),
        'Custom reply must not contain null characters'
      )
      .refine(
        (reply) =>
          new TextEncoder().encode(reply).length <=
          CALL_INTERCEPTION_REPLY_MAX_BYTES,
        'Custom reply must not exceed 16000 UTF-8 bytes'
      ),
  })
  .refine(
    (values) => !values.enabled || /[^\s\p{Cc}\p{Cf}]/u.test(values.reply),
    {
      path: ['reply'],
      message: 'A custom reply is required when call interception is enabled',
    }
  )

export type CallInterceptionFormValues = z.infer<typeof callInterceptionSchema>

export const callInterceptionConfigSchema = z.object({
  user_id: z.number().int().positive(),
  enabled: z.boolean(),
  reply: z.string(),
  revision: z.number().int().nonnegative(),
  updated_at: z.number().int().nonnegative(),
  updated_by: z.number().int().nonnegative(),
})
