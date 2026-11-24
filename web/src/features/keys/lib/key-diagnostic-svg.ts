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
const svgNS = 'http://www.w3.org/2000/svg'
const tags = new Set([
  'svg',
  'g',
  'defs',
  'title',
  'desc',
  'style',
  'path',
  'circle',
  'ellipse',
  'line',
  'polyline',
  'polygon',
  'rect',
  'text',
  'tspan',
  'linearGradient',
  'radialGradient',
  'stop',
  'clipPath',
  'mask',
  'animate',
  'animateTransform',
  'animateMotion',
])
const attributes = new Set([
  'id',
  'class',
  'viewBox',
  'preserveAspectRatio',
  'version',
  'x',
  'y',
  'x1',
  'x2',
  'y1',
  'y2',
  'width',
  'height',
  'd',
  'cx',
  'cy',
  'r',
  'rx',
  'ry',
  'points',
  'dx',
  'dy',
  'text-anchor',
  'dominant-baseline',
  'font-family',
  'font-size',
  'font-weight',
  'fill',
  'fill-opacity',
  'fill-rule',
  'stroke',
  'stroke-width',
  'stroke-opacity',
  'stroke-linecap',
  'stroke-linejoin',
  'stroke-dasharray',
  'stroke-dashoffset',
  'opacity',
  'offset',
  'stop-color',
  'stop-opacity',
  'transform',
  'gradientUnits',
  'gradientTransform',
  'spreadMethod',
  'fx',
  'fy',
  'fr',
  'clip-path',
  'clip-rule',
  'clipPathUnits',
  'mask',
  'maskUnits',
  'maskContentUnits',
])
const localRefs = new Set(['fill', 'stroke', 'clip-path', 'mask'])
const animationTags = new Set(['animate', 'animateTransform', 'animateMotion'])
const animatedAttributes = new Set([
  'opacity',
  'cx',
  'cy',
  'r',
  'rx',
  'ry',
  'x',
  'y',
  'width',
  'height',
  'fill',
  'stroke',
  'stroke-opacity',
  'fill-opacity',
  'stroke-dashoffset',
  'transform',
])
const animationAttributes = new Set([
  'attributeName',
  'type',
  'from',
  'to',
  'by',
  'values',
  'keyTimes',
  'keySplines',
  'keyPoints',
  'calcMode',
  'dur',
  'begin',
  'end',
  'repeatCount',
  'repeatDur',
  'additive',
  'accumulate',
  'rotate',
  'path',
])
const textTags = new Set(['title', 'desc', 'text', 'tspan'])
const maxSource = 512 * 1024
const cssFunctions = new Set([
  'rgb',
  'rgba',
  'hsl',
  'hsla',
  'translate',
  'translatex',
  'translatey',
  'rotate',
  'scale',
  'scalex',
  'scaley',
  'skew',
  'skewx',
  'skewy',
  'matrix',
  'cubic-bezier',
  'steps',
  'calc',
  'min',
  'max',
  'clamp',
])

// Retain ordinary SVG-owned CSS keyframes/transforms. No resource functions,
// CSS escaping, quoted strings, external CSS, or unknown @ rules survive.
function safeAnimationCss(css: string, allowKeyframes: boolean): string | null {
  if (css.length > 24_000) return null
  const stripped = css.replaceAll(/\/\*[\s\S]*?\*\//g, '')
  const checked = allowKeyframes
    ? stripped.replaceAll(/@keyframes\s+[a-z_][a-z0-9_-]*/gi, '')
    : stripped
  if (!/^[a-z0-9_.{}(),;:#%+*~>\s-]*$/i.test(checked)) return null
  if (!allowKeyframes && /[{}]/.test(checked)) return null
  for (const token of checked.matchAll(/([a-z][a-z0-9-]*)\s*\(/gi)) {
    if (!cssFunctions.has(token[1].toLowerCase())) return null
  }
  return stripped
}

// A text-only extraction. Never parse the outer returned HTML: even a detached
// HTML document can contain elements which attempt to load external resources.
function svgFragment(answer: string): string | null {
  if (answer.length > maxSource) return null
  const first = answer.search(/<svg(?:\s|>)/i)
  const last = answer.toLowerCase().lastIndexOf('</svg>')
  if (first < 0 || last <= first) return null
  const snippet = answer.slice(first, last + 6)
  if (/<\?|<!\s*(?:doctype|entity)\b/i.test(snippet)) return null
  return snippet
}

function safeValue(name: string, value: string): boolean {
  // No URI syntax at all, except a single local gradient/clip/mask reference.
  if (localRefs.has(name) && /^url\(\s*#[a-z0-9_.:-]+\s*\)$/i.test(value)) {
    return true
  }
  if (/(?:url\s*\(|[\\/:<>]|@import|expression|image-set)/i.test(value)) {
    return false
  }
  return value.length <= 24_000
}

function cleanAttribute(tag: string, name: string, value: string): boolean {
  if (animationTags.has(tag) && animationAttributes.has(name)) {
    if (name === 'attributeName') return animatedAttributes.has(value)
    if (name === 'begin' || name === 'end') {
      return /^(?:indefinite|[0-9.]+m?s)$/.test(value)
    }
    return safeValue(name, value)
  }
  if (!attributes.has(name) || !safeValue(name, value)) return false
  if (tag === 'svg' && (name === 'width' || name === 'height')) {
    return (
      /^\d{1,4}(?:\.\d+)?(?:px|%)?$/.test(value) &&
      Number.parseFloat(value) <= 4096
    )
  }
  return true
}

/** Present untrusted output only as a bounded, non-interactive SVG image. */
export function diagnosticSvgImage(answer: string): string | null {
  const source = svgFragment(answer)
  if (!source) return null
  const root = new DOMParser().parseFromString(
    source,
    'image/svg+xml'
  ).documentElement
  if (
    root.localName !== 'svg' ||
    (root.namespaceURI !== null && root.namespaceURI !== svgNS)
  ) {
    return null
  }
  const result = document.implementation.createDocument(svgNS, 'svg')
  let visited = 0
  let overflow = false

  function copy(incoming: Element, outgoing: Element, depth: number) {
    if (++visited > 4096 || depth > 40) {
      overflow = true
      return
    }
    for (const attribute of incoming.attributes) {
      if (attribute.namespaceURI) continue
      if (attribute.name === 'style') {
        const safe = safeAnimationCss(attribute.value, false)
        if (safe) outgoing.setAttribute('style', safe)
      } else if (
        cleanAttribute(incoming.localName, attribute.name, attribute.value)
      ) {
        outgoing.setAttribute(attribute.name, attribute.value)
      }
    }

    if (incoming.localName === 'style') {
      const safe = safeAnimationCss(incoming.textContent ?? '', true)
      if (safe) outgoing.append(result.createTextNode(safe))
      return
    }

    for (const child of incoming.childNodes) {
      if (
        child.nodeType === Node.TEXT_NODE &&
        textTags.has(incoming.localName)
      ) {
        outgoing.append(result.createTextNode(child.textContent ?? ''))
      } else if (
        child instanceof Element &&
        tags.has(child.localName) &&
        (child.namespaceURI === null || child.namespaceURI === svgNS)
      ) {
        if (
          animationTags.has(child.localName) &&
          child.localName !== 'animateMotion' &&
          !animatedAttributes.has(child.getAttribute('attributeName') ?? '')
        ) {
          continue
        }
        const safeChild = result.createElementNS(svgNS, child.localName)
        copy(child, safeChild, depth + 1)
        if (overflow) return
        outgoing.append(safeChild)
      }
    }
  }

  copy(root, result.documentElement, 0)
  if (
    overflow ||
    !result.documentElement.querySelector(
      'path,circle,ellipse,line,polyline,polygon,rect,text'
    )
  ) {
    return null
  }
  const sanitized = new XMLSerializer().serializeToString(result)
  return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(sanitized)}`
}
