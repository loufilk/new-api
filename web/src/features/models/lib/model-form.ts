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

import { modelDisplayCategoriesSchema } from '@/lib/model-display-categories'

import type { Model } from '../types'
import { parseModelTags as parseTagsFromUtils } from './model-utils'

// ============================================================================
// Model Form Schema
// ============================================================================

/**
 * Model form validation schema
 */
export const modelFormSchema = z.object({
  id: z.number().optional(),
  model_name: z.string().min(1, 'Model name is required'),
  description: z.string().default(''),
  icon: z.string().default(''),
  tags: z.array(z.string()).default([]),
  display_categories: modelDisplayCategoriesSchema.default([]),
  vendor_id: z.number(),
  endpoints: z.string().default(''),
  name_rule: z.number().min(0).max(3).default(0),
  status: z.boolean().default(true),
  sync_official: z.boolean().default(true),
  enable_groups: z.array(z.string()).default([]),
  quota_types: z.array(z.number()).default([]),
})

export type ModelFormValues = z.infer<typeof modelFormSchema>

export const MODEL_FORM_DEFAULT_VALUES: ModelFormValues = {
  model_name: '',
  description: '',
  icon: '',
  tags: [],
  display_categories: [],
  vendor_id: 0,
  endpoints: '',
  name_rule: 0,
  status: true,
  sync_official: true,
  enable_groups: [],
  quota_types: [],
}

// ============================================================================
// Vendor Form Schema
// ============================================================================

/**
 * Vendor form validation schema
 */
export const vendorFormSchema = z.object({
  id: z.number().optional(),
  name: z.string().min(1, 'Vendor name is required'),
  description: z.string().default(''),
  icon: z.string().default(''),
  status: z.number().default(1),
})

export type VendorFormValues = z.infer<typeof vendorFormSchema>

// ============================================================================
// Form Data Transformation
// ============================================================================

/**
 * Transform model to form default values
 */
export function transformModelToFormDefaults(model: Model): ModelFormValues {
  return {
    id: model.id,
    model_name: model.model_name,
    description: model.description || '',
    icon: model.icon || '',
    tags: parseTagsFromUtils(model.tags),
    display_categories:
      modelDisplayCategoriesSchema.safeParse(model.display_categories).data ??
      [],
    vendor_id: model.vendor_id ?? 0,
    endpoints: model.endpoints || '',
    name_rule: model.name_rule || 0,
    status: model.status === 1,
    sync_official: model.sync_official === 1,
    enable_groups: model.enable_groups || [],
    quota_types: model.quota_types || [],
  }
}

/**
 * Transform form data to model create/update payload
 */
export function transformFormDataToModelPayload(
  formData: ModelFormValues,
  savedModel?: Model | null
): Partial<Model> {
  return {
    id: formData.id,
    model_name: formData.model_name,
    description: formData.description || '',
    icon: formData.icon || '',
    tags: formatTagsArray(formData.tags),
    display_categories: formData.display_categories,
    // These catalog facts are not editable here; preserve the loaded values so
    // clearing the display override restores the original automatic categories.
    input_modalities: savedModel?.input_modalities,
    output_modalities: savedModel?.output_modalities,
    capabilities: savedModel?.capabilities,
    context_length: savedModel?.context_length,
    max_output_tokens: savedModel?.max_output_tokens,
    knowledge_cutoff: savedModel?.knowledge_cutoff,
    release_date: savedModel?.release_date,
    parameter_count: savedModel?.parameter_count,
    bonus_eligible: savedModel?.bonus_eligible,
    vendor_id: formData.vendor_id,
    endpoints: formData.endpoints || '',
    name_rule: formData.name_rule,
    status: formData.status ? 1 : 0,
    sync_official: formData.sync_official ? 1 : 0,
    enable_groups: formData.enable_groups,
    quota_types: formData.quota_types,
  }
}

// ============================================================================
// Parsing and Formatting Helpers
// ============================================================================

/**
 * Format tags array to string
 */
export function formatTagsArray(tags: string[]): string {
  return tags.filter(Boolean).join(',')
}

/**
 * Validate JSON string
 */
export function validateJSON(value: string): boolean {
  if (!value || value.trim() === '') return true

  try {
    JSON.parse(value)
    return true
  } catch {
    return false
  }
}

/**
 * Validate endpoints JSON
 */
export function validateEndpoints(endpoints: string): boolean {
  return validateJSON(endpoints)
}
