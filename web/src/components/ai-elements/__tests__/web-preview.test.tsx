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
import { render, screen } from '@testing-library/react'
import { describe, it, expect, vi } from 'vitest'

import { WebPreview, WebPreviewBody, WebPreviewConsole } from '../web-preview'

describe('WebPreviewConsole', () => {
  it('should handle duplicate logs without key warnings', () => {
    const consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
    const timestamp = new Date('2026-01-01T12:00:00Z')

    const logs = [
      { timestamp, level: 'log' as const, message: 'test message' },
      { timestamp, level: 'log' as const, message: 'test message' },
      { timestamp, level: 'log' as const, message: 'test message' },
    ]

    render(
      <WebPreview defaultUrl='https://example.com'>
        <WebPreviewBody src='https://example.com' />
        <WebPreviewConsole logs={logs} />
      </WebPreview>
    )

    expect(screen.getByText('Console')).toBeInTheDocument()
    expect(consoleSpy).not.toHaveBeenCalledWith(
      expect.stringMatching(/key.*duplicate/i),
      expect.anything()
    )

    consoleSpy.mockRestore()
  })

  it('should handle logs with same timestamp, level, and message', () => {
    const consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
    const timestamp = new Date('2026-01-01T12:00:00Z')

    const logs = [
      { timestamp, level: 'error' as const, message: 'Error occurred' },
      { timestamp, level: 'error' as const, message: 'Error occurred' },
      { timestamp, level: 'warn' as const, message: 'Warning' },
      { timestamp, level: 'warn' as const, message: 'Warning' },
    ]

    render(
      <WebPreview defaultUrl='https://example.com'>
        <WebPreviewBody src='https://example.com' />
        <WebPreviewConsole logs={logs} />
      </WebPreview>
    )

    expect(screen.getByText('Console')).toBeInTheDocument()
    expect(consoleSpy).not.toHaveBeenCalledWith(
      expect.stringMatching(/key.*duplicate/i),
      expect.anything()
    )

    consoleSpy.mockRestore()
  })

  it('should render all duplicate logs correctly', async () => {
    const timestamp = new Date('2026-01-01T12:00:00Z')

    const logs = [
      { timestamp, level: 'log' as const, message: 'same' },
      { timestamp, level: 'log' as const, message: 'same' },
      { timestamp, level: 'log' as const, message: 'same' },
    ]

    render(
      <WebPreview defaultUrl='https://example.com'>
        <WebPreviewBody src='https://example.com' />
        <WebPreviewConsole logs={logs} />
      </WebPreview>
    )

    // Open the console by clicking the Console button
    const consoleButton = screen.getByText('Console')
    await consoleButton.click()

    // All three "same" messages should be rendered
    const sameMessages = screen.getAllByText(/same/)
    expect(sameMessages).toHaveLength(3)
  })
})
