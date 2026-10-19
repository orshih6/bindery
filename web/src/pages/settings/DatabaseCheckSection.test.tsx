import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import DatabaseCheckSection from './DatabaseCheckSection'
import GeneralTab from './GeneralTab'
import { api, type DBIntegrityReport } from '../../api/client'

const auth = vi.hoisted(() => ({ isAdmin: true }))

vi.mock('../../components/ThemeToggle', () => ({ default: () => <button type="button">Theme</button> }))
vi.mock('../../components/LanguageSwitcher', () => ({ default: () => <select aria-label="Language" /> }))
vi.mock('../../auth/AuthContext', () => ({
  useAuth: () => ({
    status: { authenticated: true, username: 'u', role: auth.isAdmin ? 'admin' : 'user', mode: 'enabled', setupRequired: false },
    loading: false,
    isAdmin: auth.isAdmin,
    refresh: vi.fn(),
    logout: vi.fn(),
  }),
}))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, opts?: unknown) =>
      opts && typeof opts === 'object' ? `${key} ${JSON.stringify(opts)}` : key,
    i18n: { changeLanguage: vi.fn() },
  }),
}))
vi.mock('../../api/client', async importOriginal => {
  const actual = await importOriginal<typeof import('../../api/client')>()
  return {
    ...actual,
    api: {
      ...actual.api,
      listSettings: vi.fn(),
      libraryScanStatus: vi.fn(),
      getStorage: vi.fn(),
      authConfig: vi.fn(),
      checkDBIntegrity: vi.fn(),
    },
  }
})

beforeEach(() => {
  auth.isAdmin = true
  vi.mocked(api.checkDBIntegrity).mockReset()
  vi.mocked(api.listSettings).mockResolvedValue([])
  vi.mocked(api.libraryScanStatus).mockRejectedValue(new Error('no scan'))
  vi.mocked(api.authConfig).mockRejectedValue(new Error('no auth cfg'))
  vi.mocked(api.getStorage).mockRejectedValue(new Error('no storage'))
})

const drift: DBIntegrityReport = {
  total: 5,
  checkedAt: '2026-10-18T12:00:00Z',
  relationships: [
    { childTable: 'book_files', parentTable: 'books', onDelete: 'CASCADE', violations: 4 },
    { childTable: 'downloads', parentTable: 'books', onDelete: 'SET NULL', violations: 1 },
  ],
}

describe('DatabaseCheckSection', () => {
  it('does not scan until the button is pressed', () => {
    render(<DatabaseCheckSection />)
    expect(api.checkDBIntegrity).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: 'settings.general.dbCheckButton' })).toBeTruthy()
  })

  it('shows a loading state while the scan runs, then the table', async () => {
    let resolve!: (r: DBIntegrityReport) => void
    vi.mocked(api.checkDBIntegrity).mockReturnValue(new Promise(r => { resolve = r }))
    render(<DatabaseCheckSection />)
    fireEvent.click(screen.getByRole('button', { name: 'settings.general.dbCheckButton' }))

    const busy = await screen.findByRole('button', { name: 'settings.general.dbCheckRunning' })
    expect((busy as HTMLButtonElement).disabled).toBe(true)

    resolve(drift)
    await waitFor(() => expect(screen.getByText('book_files -> books')).toBeTruthy())
    expect(screen.getByText('downloads -> books')).toBeTruthy()
    expect(screen.getByText('4')).toBeTruthy()
    expect(screen.getByText('settings.general.dbCheckOnDeleteCascade')).toBeTruthy()
    expect(screen.getByText('settings.general.dbCheckOnDeleteSetNull')).toBeTruthy()
    expect(screen.queryByText('settings.general.dbCheckClean')).toBeNull()
    expect((screen.getByRole('button', { name: 'settings.general.dbCheckButton' }) as HTMLButtonElement).disabled).toBe(false)
  })

  it('tells apart two foreign keys from one child table to the same parent', async () => {
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
    vi.mocked(api.checkDBIntegrity).mockResolvedValue({
      total: 3,
      checkedAt: '2026-10-18T12:00:00Z',
      relationships: [
        { childTable: 'requests', parentTable: 'users', onDelete: 'CASCADE', violations: 2 },
        { childTable: 'requests', parentTable: 'users', onDelete: 'SET NULL', violations: 1 },
      ],
    })
    render(<DatabaseCheckSection />)
    fireEvent.click(screen.getByRole('button', { name: 'settings.general.dbCheckButton' }))
    await waitFor(() => expect(screen.getByRole('table')).toBeTruthy())
    const rows = screen.getAllByRole('row').slice(1)
    expect(rows).toHaveLength(2)
    expect(rows[0].textContent).toContain('(CASCADE)')
    expect(rows[1].textContent).toContain('(SET NULL)')
    // No duplicate React key warning.
    expect(errSpy.mock.calls.some(c => String(c[0]).includes('same key'))).toBe(false)
    errSpy.mockRestore()
  })

  it('shows the clean state when nothing drifted', async () => {
    vi.mocked(api.checkDBIntegrity).mockResolvedValue({ total: 0, relationships: [], checkedAt: '2026-10-18T12:00:00Z' })
    render(<DatabaseCheckSection />)
    fireEvent.click(screen.getByRole('button', { name: 'settings.general.dbCheckButton' }))
    await waitFor(() => expect(screen.getByText('settings.general.dbCheckClean')).toBeTruthy())
    expect(screen.queryByRole('table')).toBeNull()
  })

  it('shows the server message on failure and clears any earlier result', async () => {
    vi.mocked(api.checkDBIntegrity).mockResolvedValueOnce(drift)
    render(<DatabaseCheckSection />)
    fireEvent.click(screen.getByRole('button', { name: 'settings.general.dbCheckButton' }))
    await waitFor(() => expect(screen.getByRole('table')).toBeTruthy())

    vi.mocked(api.checkDBIntegrity).mockRejectedValueOnce(new Error('a database check is already running'))
    fireEvent.click(screen.getByRole('button', { name: 'settings.general.dbCheckButton' }))
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('a database check is already running'))
    expect(screen.queryByRole('table')).toBeNull()
  })

  it('points at db-repair and offers no cleanup control', () => {
    render(<DatabaseCheckSection />)
    expect(screen.getByText(/dbCheckRepairNote/)).toBeTruthy()
    const link = screen.getByRole('link', { name: 'settings.general.dbCheckDocsLink' }) as HTMLAnchorElement
    expect(link.href).toContain('docs/DEPLOYMENT.md#database-foreign-key-integrity')
    expect(screen.getAllByRole('button')).toHaveLength(1)
  })
})

describe('GeneralTab database check', () => {
  it('shows the control to admins without scanning on load', async () => {
    render(<GeneralTab />)
    // The Appearance heading shows for every role once the tab has loaded.
    await screen.findByText('settings.general.appearance')
    expect(screen.getByRole('button', { name: 'settings.general.dbCheckButton' })).toBeTruthy()
    expect(api.checkDBIntegrity).not.toHaveBeenCalled()
  })

  it('hides the control from non admins', async () => {
    auth.isAdmin = false
    render(<GeneralTab />)
    await screen.findByText('settings.general.appearance')
    expect(screen.queryByRole('button', { name: 'settings.general.dbCheckButton' })).toBeNull()
    expect(screen.queryByText('settings.general.dbCheck')).toBeNull()
    expect(api.checkDBIntegrity).not.toHaveBeenCalled()
  })
})
