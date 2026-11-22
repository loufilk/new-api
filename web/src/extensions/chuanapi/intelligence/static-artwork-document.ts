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
import DOMPurify from 'dompurify'

// Defense in depth: sanitize, then isolate in an opaque-origin, scriptless frame.
// No model content is ever inserted into the parent DOM or served as same-origin HTML.
const artworkCSP =
  "default-src 'none'; script-src 'none'; style-src 'unsafe-inline'; img-src 'none'; font-src 'none'; connect-src 'none'; media-src 'none'; object-src 'none'; frame-src 'none'; form-action 'none'; base-uri 'none'; navigate-to 'none'"
export function buildStaticArtworkDocument(html: string): string {
  const clean = DOMPurify.sanitize(html.slice(0, 262144), {
    USE_PROFILES: { html: true, svg: true },
    ADD_TAGS: ['style'],
    FORCE_BODY: true,
    FORBID_TAGS: [
      'script',
      'iframe',
      'frame',
      'object',
      'embed',
      'link',
      'meta',
      'base',
      'form',
      'input',
      'button',
      'textarea',
      'select',
      'a',
      'image',
      'img',
      'video',
      'audio',
      'source',
      'foreignObject',
      'animate',
      'animateMotion',
      'animateTransform',
      'set',
      'use',
    ],
    FORBID_ATTR: [
      'href',
      'xlink:href',
      'src',
      'srcset',
      'srcdoc',
      'action',
      'formaction',
      'target',
      'download',
      'ping',
    ],
    ALLOW_DATA_ATTR: false,
  })
  return `<!doctype html><html><head><meta http-equiv="Content-Security-Policy" content="${artworkCSP}"><meta name="referrer" content="no-referrer"><style>html,body{margin:0;min-height:100%;overflow:auto}*{box-sizing:border-box}</style></head><body>${clean}</body></html>`
}
