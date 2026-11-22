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
// Announcements deliberately accept public HTTPS images, not uploads or data URLs.
export function getAnnouncementImageUrl(source: string): string | null {
  if (
    !source ||
    source.length > 4096 ||
    source.includes('\\') ||
    [...source].some(
      (character) =>
        character.charCodeAt(0) <= 32 || character.charCodeAt(0) === 127
    )
  ) {
    return null
  }
  try {
    const url = new URL(source)
    if (
      url.protocol !== 'https:' ||
      url.href.length > 4096 ||
      url.username ||
      url.password ||
      /\.svgz?$/i.test(decodeURIComponent(url.pathname))
    ) {
      return null
    }
    return url.href
  } catch {
    return null
  }
}

export function announcementImageFallback(
  alt: string,
  message: string
): HTMLSpanElement {
  const fallback = document.createElement('span')
  fallback.className = 'text-muted-foreground break-words'
  fallback.textContent = alt ? `${message}: ${alt}` : message
  return fallback
}

export function hardenAnnouncementImages(
  html: string,
  message: string
): string {
  const template = document.createElement('template')
  template.innerHTML = html
  template.content.querySelectorAll('img').forEach((image) => {
    const url = getAnnouncementImageUrl(image.getAttribute('src') ?? '')
    const alt = image.getAttribute('alt') || ''
    if (!url) {
      image.replaceWith(announcementImageFallback(alt, message))
      return
    }
    const title = image.getAttribute('title')
    // Do not let HTML dimensions, srcset, styling or image maps bypass this policy.
    for (const name of image.getAttributeNames()) {
      image.removeAttribute(name)
    }
    image.setAttribute('src', url)
    image.setAttribute('alt', alt)
    if (title) image.setAttribute('title', title)
    image.setAttribute('referrerpolicy', 'no-referrer')
    image.setAttribute('loading', 'lazy')
    image.setAttribute('decoding', 'async')
    image.className = 'block max-w-full h-auto max-h-[70vh] object-contain'
  })
  return template.innerHTML
}
