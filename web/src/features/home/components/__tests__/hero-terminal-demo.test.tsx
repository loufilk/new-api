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

import { HeroTerminalDemo } from '../hero-terminal-demo'

describe('HeroTerminalDemo', () => {
  it('should render without duplicate key warnings', () => {
    const consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {})

    render(<HeroTerminalDemo />)

    expect(screen.getByText(/curl/i)).toBeInTheDocument()
    expect(consoleSpy).not.toHaveBeenCalledWith(
      expect.stringMatching(/key.*duplicate/i),
      expect.anything()
    )

    consoleSpy.mockRestore()
  })

  it('should handle demos with duplicate response lines correctly', () => {
    const consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {})

    const { container } = render(<HeroTerminalDemo />)

    // Response block should be present
    expect(screen.getByText('Response')).toBeInTheDocument()

    // Check that response content is rendered
    const codeContent = container.textContent
    expect(codeContent).toContain('{')
    expect(codeContent).toContain('}')

    // No duplicate key warnings
    expect(consoleSpy).not.toHaveBeenCalledWith(
      expect.stringMatching(/key.*duplicate/i),
      expect.anything()
    )

    consoleSpy.mockRestore()
  })

  it('should render all demo tabs', () => {
    render(<HeroTerminalDemo />)

    expect(screen.getByText('Chat')).toBeInTheDocument()
    expect(screen.getByText('Responses')).toBeInTheDocument()
    expect(screen.getByText('Claude')).toBeInTheDocument()
    expect(screen.getByText('Gemini')).toBeInTheDocument()
  })

  it('should have stable keys for request and response lines', () => {
    const consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {})

    const { rerender } = render(<HeroTerminalDemo />)

    // Re-render should not trigger key warnings
    rerender(<HeroTerminalDemo />)

    expect(consoleSpy).not.toHaveBeenCalledWith(
      expect.stringMatching(/key.*duplicate/i),
      expect.anything()
    )

    consoleSpy.mockRestore()
  })
})
