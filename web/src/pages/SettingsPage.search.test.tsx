import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'
import SettingsPage from './SettingsPage'
import { api } from '../api/client'

// Settings search (#3059): finds a tab, a section heading or a setting key and
// jumps there. The i18n mock resolves keys against the real en.json (and an
// optional override map standing in for another locale), so the test sees the
// strings a user sees rather than raw keys.
const state = vi.hoisted(() => ({ isAdmin: true, overrides: {} as Record<string, string> }))

vi.mock('../auth/AuthContext', () => ({
  useAuth: () => ({ isAdmin: state.isAdmin, status: { authenticated: true, role: state.isAdmin ? 'admin' : 'user' } }),
}))
vi.mock('react-i18next', async () => {
  const en = (await import('../i18n/locales/en.json')).default as Record<string, unknown>
  const lookup = (key: string): string | undefined => {
    if (state.overrides[key]) return state.overrides[key]
    const v = key.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown> | undefined)?.[k], en)
    return typeof v === 'string' ? v : undefined
  }
  const t = (key: string, opts?: unknown) => {
    const fallback = typeof opts === 'string' ? opts : undefined
    if (opts && typeof opts === 'object' && 'count' in opts) {
      const n = (opts as { count: number }).count
      return (lookup(`${key}_${n === 1 ? 'one' : 'other'}`) ?? key).replace('{{count}}', String(n))
    }
    return lookup(key) ?? fallback ?? key
  }
  const value = { t, i18n: { changeLanguage: vi.fn() } }
  return { useTranslation: () => value }
})
vi.mock('../api/client', async importOriginal => {
  const actual = await importOriginal<typeof import('../api/client')>()
  return {
    ...actual,
    api: {
      ...actual.api,
      listIndexers: vi.fn().mockResolvedValue([]),
      listDownloadClients: vi.fn().mockResolvedValue([]),
      listProwlarr: vi.fn().mockResolvedValue([]),
      listSettingDescriptors: vi.fn(),
    },
  }
})
vi.mock('../components/ProtocolMismatchWarning', () => ({ default: () => null }))

vi.mock('./settings/GeneralTab', () => ({ default: () => <p>general tab</p> }))
vi.mock('./settings/AboutTab', () => ({ default: () => <p>about tab</p> }))
vi.mock('./settings/IndexersTab', () => ({ default: () => <p>indexers tab</p> }))
vi.mock('./settings/ClientsTab', () => ({ default: () => <p>clients tab</p> }))
vi.mock('./settings/NotificationsTab', () => ({ default: () => <p>notifications tab</p> }))
vi.mock('./settings/QualityTab', () => ({ default: () => <p>quality tab</p> }))
vi.mock('./settings/MetadataTab', () => ({ default: () => <p>metadata tab</p> }))
vi.mock('./settings/RootFoldersTab', () => ({ default: () => <p>rootfolders tab</p> }))
vi.mock('./settings/CalibreTab', () => ({ default: () => <p>calibre tab</p> }))
vi.mock('./settings/ABSTab', () => ({ default: () => <p>abs tab</p> }))
vi.mock('./settings/GrimmoryTab', () => ({ default: () => <p>grimmory tab</p> }))
vi.mock('./settings/ApiKeysTab', () => ({ default: () => <p>api keys tab</p> }))
vi.mock('./settings/ImportTab', () => ({ default: () => <p>import tab</p> }))
vi.mock('./settings/BlocklistTab', () => ({ default: () => <p>blocklist tab</p> }))
vi.mock('./settings/AdvancedTab', () => ({ default: () => <p>advanced tab</p> }))
// The Logs stub renders its Telemetry heading late, as the real tab does after
// its first fetch, so the scroll has to wait for it.
vi.mock('./settings/LogsTab', async () => {
  const { useEffect, useState } = await import('react')
  return {
    default: function LogsStub() {
      const [ready, setReady] = useState(false)
      useEffect(() => { const id = setTimeout(() => setReady(true), 30); return () => clearTimeout(id) }, [])
      return <div><p>logs tab</p>{ready && <h3>Usage Telemetry</h3>}</div>
    },
  }
})

function Where() {
  const l = useLocation()
  return <output data-testid="where">{l.search}</output>
}

function renderSettings() {
  return render(
    <MemoryRouter initialEntries={['/settings']}>
      <Routes><Route path="/settings" element={<SettingsPage />} /></Routes>
      <Where />
    </MemoryRouter>,
  )
}

const box = () => screen.getByRole('combobox', { name: 'Find a settings tab or section' })
const type = (text: string) => fireEvent.change(box(), { target: { value: text } })
const status = () => document.querySelector('p[role="status"]') as HTMLElement
const optionNames = () => screen.queryAllByRole('option').map(o => o.textContent)

beforeEach(() => {
  state.isAdmin = true
  state.overrides = {}
  vi.mocked(api.listSettingDescriptors).mockResolvedValue([
    { key: 'search.wanted_interval', state: 'active' },
    { key: 'legacy.dead_key', state: 'inert' },
  ] as never)
})

describe('Settings search', () => {
  it('jumps to a tab by its name with Enter on the first result', async () => {
    renderSettings()
    type('blocklist')
    expect(optionNames()).toEqual(['Blocklist'])
    fireEvent.keyDown(box(), { key: 'Enter' })
    expect(await screen.findByText('blocklist tab')).toBeInTheDocument()
    expect(screen.getByTestId('where')).toHaveTextContent('tab=blocklist')
    expect(box()).toHaveValue('')
  })

  it('finds a section heading, switches to its tab and focuses the heading once it renders', async () => {
    renderSettings()
    type('telemetry')
    expect(optionNames()).toEqual(['Usage TelemetryLogs'])
    fireEvent.keyDown(box(), { key: 'Enter' })
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent('tab=logs'))
    const heading = await screen.findByRole('heading', { name: 'Usage Telemetry' })
    await waitFor(() => expect(heading).toHaveFocus())
  })

  it('matches a heading in the active language, not just English', () => {
    state.overrides = { 'settings.general.fileNaming': 'Dateibenennung' }
    renderSettings()
    type('dateiben')
    expect(optionNames()).toEqual(['DateibenennungGeneral'])
    type('file naming')
    expect(optionNames()).toEqual([])
  })

  it('finds an active setting key and opens Advanced, ignoring inert keys', async () => {
    renderSettings()
    fireEvent.focus(box())
    await waitFor(() => expect(api.listSettingDescriptors).toHaveBeenCalledTimes(1))
    type('wanted_int')
    await waitFor(() => expect(optionNames()).toEqual(['search.wanted_intervalAdvanced']))
    type('dead_key')
    expect(optionNames()).toEqual([])
    type('wanted_int')
    fireEvent.keyDown(box(), { key: 'Enter' })
    expect(await screen.findByText('advanced tab')).toBeInTheDocument()
  })

  it('moves with the arrow keys, exposes the active option and announces the count', () => {
    renderSettings()
    type('log')
    const options = screen.getAllByRole('option')
    expect(options.length).toBeGreaterThan(1)
    expect(box()).toHaveAttribute('aria-activedescendant', options[0].id)
    fireEvent.keyDown(box(), { key: 'ArrowDown' })
    expect(box()).toHaveAttribute('aria-activedescendant', options[1].id)
    expect(options[1]).toHaveAttribute('aria-selected', 'true')
    expect(status()).toHaveTextContent(`${options.length} results`)
  })

  it('clears with Escape and says when nothing matches', () => {
    renderSettings()
    type('zzzz')
    expect(status()).toHaveTextContent('No matching tab or section')
    fireEvent.keyDown(box(), { key: 'Escape' })
    expect(box()).toHaveValue('')
    expect(status()).toHaveTextContent('')
  })

  it('focuses the box on / unless typing in a field', () => {
    renderSettings()
    fireEvent.keyDown(document.body, { key: '/' })
    expect(box()).toHaveFocus()
  })

  it('never offers admin tabs, headings or keys to a non admin', () => {
    state.isAdmin = false
    renderSettings()
    type('telemetry')
    expect(optionNames()).toEqual([])
    type('blocklist')
    expect(optionNames()).toEqual([])
    type('appearance')
    expect(optionNames()).toEqual(['AppearanceGeneral'])
    fireEvent.focus(box())
    expect(api.listSettingDescriptors).not.toHaveBeenCalled()
  })
})
