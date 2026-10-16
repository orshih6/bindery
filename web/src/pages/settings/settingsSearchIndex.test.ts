import { describe, it, expect } from 'vitest'
import en from '../../i18n/locales/en.json'
import { HEADINGS, MAX_RESULTS, searchSettings, SettingsTabId } from './settingsSearchIndex'

const ALL: { id: SettingsTabId; label: string }[] = [
  { id: 'general', label: 'General' },
  { id: 'about', label: 'About' },
  { id: 'indexers', label: 'Indexers' },
  { id: 'logs', label: 'Logs' },
  { id: 'advanced', label: 'Advanced' },
  { id: 'metadata', label: 'Metadata' },
]

// A stand in for another locale: distinct names, one with diacritics.
const names: Record<string, string> = {
  'settings.general.appearance': 'Darstellung Bücher',
  'settings.general.fileNaming': 'Zeta Dateinamen',
  'settings.general.downloads': 'Alpha Dateinamen',
  'settings.general.storage': 'Speicher',
}
const t = (key: string, fallback?: string) => names[key] ?? fallback ?? key

const run = (query: string, extra: Partial<Parameters<typeof searchSettings>[0]> = {}) =>
  searchSettings({ query, tabs: ALL, t, isAdmin: true, ...extra })
const ids = (query: string, extra: Partial<Parameters<typeof searchSettings>[0]> = {}) =>
  run(query, extra).results.map(r => r.id)

describe('searchSettings ranking', () => {
  it('ranks a prefix match above a word prefix above a substring', () => {
    // Listed worst first, so only the rank can put them in order.
    const tabs = [
      { id: 'logs' as const, label: 'Bigzzq' }, // substring
      { id: 'indexers' as const, label: 'Zebra zzq' }, // word prefix
      { id: 'general' as const, label: 'Zzqbook' }, // prefix
    ]
    const r = searchSettings({ query: 'zzq', tabs, t, isAdmin: true }).results
    expect(r.map(x => x.id)).toEqual(['tab:general', 'tab:indexers', 'tab:logs'])
  })

  it('keeps index order within one rank', () => {
    // Both headings are word prefix matches; Zeta is listed first in HEADINGS.
    expect(ids('dateinamen')).toEqual(['h:general:settings.general.fileNaming', 'h:general:settings.general.downloads'])
  })

  it('lists tabs and headings before setting keys, even for a better textual key match', () => {
    const r = run('log', { settingKeys: ['catalog.size', 'log.level'] }).results
    expect(r[0]).toMatchObject({ kind: 'tab', tab: 'logs' })
    const firstKey = r.findIndex(x => x.kind === 'key')
    expect(firstKey).toBeGreaterThan(0)
    expect(r.slice(firstKey).every(x => x.kind === 'key')).toBe(true)
    // among keys, a prefix match still beats a substring match
    expect(r.filter(x => x.kind === 'key').map(x => x.id)).toEqual(['k:log.level', 'k:catalog.size'])
  })

  it('puts a substring heading match ahead of a prefix key match', () => {
    // The key offset must outweigh a heading's worst rank (substring, 2).
    const tabs = [{ id: 'advanced' as const, label: 'Advanced' }, { id: 'general' as const, label: 'General' }]
    const r = searchSettings({ query: 'stellung', tabs, t, isAdmin: true, settingKeys: ['stellung.x'] }).results
    expect(r.map(x => x.kind)).toEqual(['heading', 'key'])
  })

  it('offers keys only when Advanced is a tab the viewer has', () => {
    const noAdv = ALL.filter(x => x.id !== 'advanced')
    const out = searchSettings({ query: 'log', tabs: noAdv, t, isAdmin: false, settingKeys: ['log.level'] })
    expect(out.results.some(x => x.kind === 'key')).toBe(false)
  })

  it('folds diacritics in both the query and the heading', () => {
    expect(ids('bucher')).toEqual(['h:general:settings.general.appearance'])
    expect(ids('BÜCHER')).toEqual(['h:general:settings.general.appearance'])
  })

  it('trims and collapses whitespace in the query', () => {
    expect(ids('  zeta    dateinamen ')).toEqual(['h:general:settings.general.fileNaming'])
  })

  it('returns nothing for a blank query', () => {
    expect(run('   ')).toEqual({ results: [], total: 0 })
  })

  it('caps the list and reports the real total', () => {
    const settingKeys = Array.from({ length: 20 }, (_v, i) => `bulk.key${i}`)
    const out = run('bulk', { settingKeys })
    expect(out.results).toHaveLength(MAX_RESULTS)
    expect(out.total).toBe(20)
  })

  it('does not repeat a tab as its own heading', () => {
    // settings.advanced.title falls back to "Advanced", the tab's own name.
    expect(ids('advanced')).toEqual(['tab:advanced'])
  })

  it('hides adminOnly headings from a non admin even when the tab is open to them', () => {
    expect(ids('speicher', { isAdmin: false })).toEqual([])
    expect(ids('speicher', { isAdmin: true })).toEqual(['h:general:settings.general.storage'])
  })
})

describe('HEADINGS', () => {
  const lookup = (key: string) => key.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown> | undefined)?.[k], en)

  it('only names keys that exist in en.json or carry a fallback', () => {
    for (const h of HEADINGS) {
      if (h.key) expect(typeof lookup(h.key) === 'string' || !!h.fallback, h.key).toBe(true)
    }
  })

  // Drift guard: every heading a tab renders must be in the index (or be listed
  // here with a reason), and every indexed heading must still be rendered.
  const sources = import.meta.glob('./*{Tab,Section}.tsx', { query: '?raw', import: 'default', eager: true }) as Record<string, string>
  const files = Object.keys(sources)
  const keyRe = /<h[34][^>]*>\s*\{t\(\s*'([^']+)'/g
  const litRe = /<h[34][^>]*>\s*([A-Z][A-Za-z() -]+?)\s*<\/h[34]>/g

  // Headings that are deliberately not indexed, with the reason.
  const ALLOWED_KEYS: Record<string, string> = {
    'settings.calibre.runs.modalTitle': 'a modal title carrying a run id, not a page section',
  }
  const ALLOWED_LITERALS: Record<string, string> = {
    Calibre: 'the tab title, covered by the tab result',
    Audiobookshelf: 'the tab title, covered by the tab result',
    Grimmory: 'the tab title, covered by the tab result',
  }

  const sourceKeys = new Set<string>()
  const sourceLiterals = new Set<string>()
  for (const src of Object.values(sources)) {
    for (const m of src.matchAll(keyRe)) sourceKeys.add(m[1])
    for (const m of src.matchAll(litRe)) sourceLiterals.add(m[1])
  }

  it('finds the tab sources it is meant to scan', () => {
    expect(files.length).toBeGreaterThan(10)
    expect(sourceKeys.size).toBeGreaterThan(20)
  })

  it('indexes every heading the tabs render through t()', () => {
    const indexed = new Set(HEADINGS.map(h => h.key).filter(Boolean))
    expect([...sourceKeys].filter(k => !indexed.has(k) && !ALLOWED_KEYS[k])).toEqual([])
  })

  it('indexes every literal heading the tabs render', () => {
    const indexed = new Set(HEADINGS.map(h => h.literal).filter(Boolean))
    expect([...sourceLiterals].filter(l => !indexed.has(l) && !ALLOWED_LITERALS[l])).toEqual([])
  })

  it('does not index a heading no tab renders any more', () => {
    for (const h of HEADINGS) {
      if (h.key) expect(sourceKeys.has(h.key), h.key).toBe(true)
      if (h.literal) expect(sourceLiterals.has(h.literal), h.literal).toBe(true)
    }
  })
})
