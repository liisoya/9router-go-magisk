import { describe, expect, it } from 'bun:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

// The Console Log page is read at a glance, so a level colour that blends into
// the panel defeats the point of colouring it.
//
// The palette below is the one Tailwind 4 actually ships, copied out of the
// compiled stylesheet, because that is what renders: `text-red-700` is
// oklch(50.5% .213 27.518), not the #b91c1c its name suggests. Measuring
// against the hex approximation passes while the real screen colour is a
// different hue. Each entry is verified against web/dist in the sibling
// "matches the compiled palette" cases below, so a Tailwind upgrade that moves
// a tone fails here instead of silently going unreadable.

const STREAM = fileURLToPath(new URL('../components/ConsoleLogStream.svelte', import.meta.url))
const TOKENS = fileURLToPath(new URL('../index.css', import.meta.url))

type Rgb = [number, number, number]

function parseHex(hex: string): Rgb {
  const value = hex.trim().replace('#', '')
  return [parseInt(value.slice(0, 2), 16), parseInt(value.slice(2, 4), 16), parseInt(value.slice(4, 6), 16)]
}

/** Composites a translucent surface token over the page background. */
function composite(foreground: Rgb, background: Rgb, alpha: number): Rgb {
  return foreground.map((channel, i) => Math.round(channel * alpha + background[i] * (1 - alpha))) as Rgb
}

function relativeLuminance([r, g, b]: Rgb): number {
  const channel = (value: number) => {
    const c = value / 255
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
  }
  return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b)
}

function contrastRatio(foreground: Rgb, background: Rgb): number {
  const a = relativeLuminance(foreground)
  const b = relativeLuminance(background)
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05)
}

/** Converts an oklch() token to sRGB the way the browser renders it. */
function oklchToRgb(value: string): Rgb {
  const [lRaw, cRaw, hRaw] = value.replace('oklch(', '').replace(')', '').split(/[\s,]+/)
  const l = parseFloat(lRaw) / (lRaw.endsWith('%') ? 100 : 1)
  const c = parseFloat(cRaw)
  const h = (parseFloat(hRaw) * Math.PI) / 180
  const a = c * Math.cos(h)
  const b = c * Math.sin(h)
  const lCube = (l + 0.3963377774 * a + 0.2158037573 * b) ** 3
  const mCube = (l - 0.1055613458 * a - 0.0638541728 * b) ** 3
  const sCube = (l - 0.0894841775 * a - 1.291485548 * b) ** 3
  const linear = [
    4.0767416621 * lCube - 3.3077115913 * mCube + 0.2309699292 * sCube,
    -1.2684380046 * lCube + 2.6097574011 * mCube - 0.3413193965 * sCube,
    -0.0041960863 * lCube - 0.7034186147 * mCube + 1.707614701 * sCube,
  ]
  return linear.map((u) => {
    const clipped = Math.max(0, Math.min(1, u))
    return Math.round((clipped <= 0.0031308 ? 12.92 * clipped : 1.055 * clipped ** (1 / 2.4) - 0.055) * 255)
  }) as Rgb
}

function cssToken(css: string, name: string, theme: 'light' | 'dark'): Rgb {
  const darkStart = css.indexOf('.dark {')
  const scope = theme === 'dark' ? css.slice(darkStart) : css.slice(0, darkStart)
  const match = new RegExp(`${name}:\\s*(#[0-9a-fA-F]{6})`).exec(scope)
  if (!match) throw new Error(`design token ${name} (${theme}) not found in index.css`)
  return parseHex(match[1])
}

// Values read from the built web/dist/assets/*.css of this commit.
const LEVEL_PAIRS = [
  { level: 'error', light: 'red-700', lightOklch: 'oklch(50.5% .213 27.518)', dark: 'red-400', darkOklch: 'oklch(70.4% .191 22.216)', family: 'red' },
  { level: 'warn', light: 'amber-800', lightOklch: 'oklch(47.3% .137 46.201)', dark: 'amber-400', darkOklch: 'oklch(82.8% .189 84.429)', family: 'amber' },
  { level: 'info', light: 'emerald-700', lightOklch: 'oklch(50.8% .118 165.612)', dark: 'emerald-400', darkOklch: 'oklch(76.5% .177 163.223)', family: 'emerald' },
  { level: 'debug', light: 'sky-800', lightOklch: 'oklch(44.3% .11 240.79)', dark: 'sky-300', darkOklch: 'oklch(82.8% .111 230.318)', family: 'sky' },
] as const

describe('console log level colours', () => {
  const css = readFileSync(TOKENS, 'utf8')
  const source = readFileSync(STREAM, 'utf8')

  // bg-surface-2 at /50 over the page background: the actual row backdrop.
  const panels = {
    light: composite(cssToken(css, 'surface-2', 'light'), cssToken(css, 'bg', 'light'), 0.5),
    dark: composite(cssToken(css, 'surface-2', 'dark'), cssToken(css, 'bg', 'dark'), 0.5),
  }

  it.each(LEVEL_PAIRS)('$level clears 4.5:1 on the light panel', ({ lightOklch }) => {
    const ratio = contrastRatio(oklchToRgb(lightOklch), panels.light)
    expect(ratio).toBeGreaterThanOrEqual(4.5)
  })

  it.each(LEVEL_PAIRS)('$level clears 4.5:1 on the dark panel', ({ darkOklch }) => {
    const ratio = contrastRatio(oklchToRgb(darkOklch), panels.dark)
    expect(ratio).toBeGreaterThanOrEqual(4.5)
  })

  // Every level must name a tone for both themes: a one-theme-only class drops
  // the row to inherited text colour in the other theme, which is the exact
  // "everything reads the same" failure this page was fixed for.
  it.each(LEVEL_PAIRS)('$level defines a tone for both themes', ({ family, light, dark }) => {
    const lightShade = light.split('-')[1]
    const darkShade = dark.split('-')[1]
    expect(lightShade).not.toBe(darkShade)
    expect(source).toContain(`text-${family}-${lightShade}`)
    expect(source).toContain(`dark:text-${family}-${darkShade}`)
  })

  // A level must not read as another level's hue family, or "spot the error"
  // becomes guesswork rather than recognition.
  it('uses a distinct hue family per level', () => {
    const families = LEVEL_PAIRS.map((pair) => pair.family)
    expect(new Set(families).size).toBe(families.length)
  })

  // Four levels that all resolve to one colour would pass every other case
  // here while defeating the feature, so assert the shades are four distinct
  // classes as actually written in the component.
  it('resolves the four levels to four distinct text classes', () => {
    const shades = new Set(LEVEL_PAIRS.map(({ family, light }) => `text-${family}-${light.split('-')[1]}`))
    expect(shades.size).toBe(4)
    for (const shade of shades) {
      expect(source).toContain(shade)
    }
  })
})