import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useState } from 'react'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import ClientsTab from './ClientsTab'
import { addRemapRule } from './helpers'
import { api, type DownloadClient } from '../../api/client'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, opts?: unknown) => typeof opts === 'string' ? opts : key,
    i18n: { changeLanguage: vi.fn() },
  }),
}))

vi.mock('../../api/client', async importOriginal => {
  const actual = await importOriginal<typeof import('../../api/client')>()
  return {
    ...actual,
    api: {
      ...actual.api,
      updateDownloadClient: vi.fn(),
      testDownloadClient: vi.fn(),
    },
  }
})

function makeClient(overrides: Partial<DownloadClient> = {}): DownloadClient {
  return {
    id: 3,
    name: 'qBit',
    type: 'qbittorrent',
    host: 'qbit',
    port: 8080,
    apiKey: '',
    username: 'u',
    password: '',
    apiKeyConfigured: false,
    passwordConfigured: true,
    useSsl: false,
    urlBase: '',
    category: 'books',
    categoryAudiobook: '',
    pathRemap: '',
    enabled: true,
    ...overrides,
  }
}

function Harness({ initial }: { initial: DownloadClient[] }) {
  const [clients, setClients] = useState(initial)
  return <ClientsTab clients={clients} setClients={setClients} />
}

const warning = {
  message: 'Connected',
  pathVisibility: { status: 'warning' as const, message: 'cannot read it', path: '/downloads/books', remotePath: '/downloads/books' },
  remapCandidates: [
    { path: '/data/downloads', source: 'downloadDir' as const, before: '/downloads/books/Example Book', after: '/data/downloads/Example Book' },
    { path: '/books', source: 'library' as const, before: '/downloads/books/Example Book', after: '/books/Example Book' },
  ],
}

async function runTest() {
  fireEvent.click(screen.getByRole('button', { name: 'common.test' }))
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.updateDownloadClient).mockImplementation(async (id, data) => makeClient({ ...data, id }))
})

describe('path remap assistant', () => {
  it('appears for a warning on a supported client with a labelled chooser and preview', async () => {
    vi.mocked(api.testDownloadClient).mockResolvedValue(warning)
    render(<Harness initial={[makeClient()]} />)
    await runTest()

    expect(await screen.findByRole('region', { name: 'settings.clients.remapAssist.heading' })).toBeTruthy()
    expect(screen.getByText('/downloads/books', { selector: 'code' })).toBeTruthy()
    const chooser = screen.getByLabelText('settings.clients.remapAssist.chooseLabel') as HTMLSelectElement
    expect(chooser.value).toBe('/data/downloads')
    expect(screen.getByText('/data/downloads/Example Book')).toBeTruthy()

    fireEvent.change(chooser, { target: { value: '/books' } })
    expect(screen.getByText('/books/Example Book')).toBeTruthy()
    expect(screen.queryByText('/data/downloads/Example Book')).toBeNull()
  })

  it.each(['sabnzbd', 'transmission', 'deluge'])('never appears for %s, even if candidates arrive', async type => {
    vi.mocked(api.testDownloadClient).mockResolvedValue(warning)
    render(<Harness initial={[makeClient({ type })]} />)
    await runTest()

    await screen.findByText('cannot read it')
    expect(screen.queryByRole('region', { name: 'settings.clients.remapAssist.heading' })).toBeNull()
  })

  it('stays away when the folder is visible or the server sends no candidates', async () => {
    vi.mocked(api.testDownloadClient).mockResolvedValue({ message: 'ok', pathVisibility: { status: 'ok', remotePath: '/downloads/books' } })
    render(<Harness initial={[makeClient()]} />)
    await runTest()
    await waitFor(() => expect(api.testDownloadClient).toHaveBeenCalled())
    expect(screen.queryByRole('region', { name: 'settings.clients.remapAssist.heading' })).toBeNull()
  })

  it('saves through the update call, keeps existing rules, then re-runs Test', async () => {
    vi.mocked(api.testDownloadClient).mockResolvedValueOnce(warning)
    vi.mocked(api.testDownloadClient).mockResolvedValueOnce({ message: 'ok', pathVisibility: { status: 'ok', remotePath: '/downloads/books' } })
    render(<Harness initial={[makeClient({ pathRemap: '/seed:/other' })]} />)
    await runTest()

    fireEvent.click(await screen.findByRole('button', { name: 'settings.clients.remapAssist.apply' }))

    await waitFor(() => expect(api.testDownloadClient).toHaveBeenCalledTimes(2))
    expect(api.updateDownloadClient).toHaveBeenCalledTimes(1)
    expect(api.updateDownloadClient).toHaveBeenCalledWith(3, expect.objectContaining({ pathRemap: '/seed:/other,/downloads/books:/data/downloads' }))
    // The update happened before the second Test.
    const updateOrder = vi.mocked(api.updateDownloadClient).mock.invocationCallOrder[0]
    expect(updateOrder).toBeGreaterThan(vi.mocked(api.testDownloadClient).mock.invocationCallOrder[0])
    expect(updateOrder).toBeLessThan(vi.mocked(api.testDownloadClient).mock.invocationCallOrder[1])
    // Re-test came back visible, so the assistant is gone.
    await waitFor(() => expect(screen.queryByRole('region', { name: 'settings.clients.remapAssist.heading' })).toBeNull())
  })

  it('shows the error and does not re-test when the save fails', async () => {
    vi.mocked(api.testDownloadClient).mockResolvedValue(warning)
    vi.mocked(api.updateDownloadClient).mockRejectedValue(new Error('pair 1 is bad'))
    render(<Harness initial={[makeClient()]} />)
    await runTest()

    fireEvent.click(await screen.findByRole('button', { name: 'settings.clients.remapAssist.apply' }))
    await waitFor(() => expect(screen.getAllByRole('alert').some(a => a.textContent?.includes('settings.clients.remapAssist.failed'))).toBe(true))
    expect(api.testDownloadClient).toHaveBeenCalledTimes(1)
  })
})

describe('addRemapRule', () => {
  it('builds remote:local, appends, and does not repeat a rule', () => {
    expect(addRemapRule('', '/downloads', '/data')).toBe('/downloads:/data')
    expect(addRemapRule('/a:/b', '/downloads', '/data')).toBe('/a:/b,/downloads:/data')
    expect(addRemapRule('/downloads:/data', '/downloads', '/data')).toBe('/downloads:/data')
  })

  it('keeps Windows drive and share paths intact on the client side', () => {
    expect(addRemapRule('', 'S:/Downloads/books', '/data/downloads')).toBe('S:/Downloads/books:/data/downloads')
    expect(addRemapRule('', '//nas/books/dl', '/data')).toBe('//nas/books/dl:/data')
  })
})
