import { useEffect, useId, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { searchSettings, SearchResult, SettingsTabId } from './settingsSearchIndex'

// A search box at the top of Settings that jumps to a tab or section heading
// (#3059). It is a combobox over a listbox (ARIA 1.2): focus stays in the input,
// the highlighted option is named by aria-activedescendant, and a polite live
// region announces how many results there are.

export interface SettingsSearchPick {
  tab: SettingsTabId
  /** Heading text to scroll to once the tab has rendered. */
  heading?: string
}

interface Props {
  tabs: { id: SettingsTabId; label: string }[]
  /** Registered setting keys (admins only), so a key quoted in docs finds Advanced. */
  settingKeys?: string[]
  onPick: (pick: SettingsSearchPick) => void
  /** Called when the box is first focused, so the parent can load keys lazily. */
  onActivate?: () => void
}

export default function SettingsSearch({ tabs, settingKeys, onPick, onActivate }: Props) {
  const { t } = useTranslation()
  const uid = useId()
  const inputId = `${uid}-input`
  const listId = `${uid}-list`
  const inputRef = useRef<HTMLInputElement>(null)
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(0)

  const results = useMemo(
    () => searchSettings({ query, tabs, settingKeys, t: (key, fallback) => t(key, fallback ?? key) }),
    [query, tabs, settingKeys, t],
  )

  // "/" focuses the box, unless the person is already typing somewhere.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== '/' || e.ctrlKey || e.metaKey || e.altKey) return
      const el = e.target as HTMLElement | null
      if (el && (el.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName))) return
      e.preventDefault()
      inputRef.current?.focus()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [])

  const pick = (r: SearchResult) => {
    setQuery('')
    setActive(0)
    onPick({ tab: r.tab, heading: r.kind === 'heading' ? r.heading : undefined })
  }

  const open = query.trim() !== ''
  const showList = open && results.length > 0
  const current = Math.min(active, Math.max(results.length - 1, 0))
  const optionId = (i: number) => `${listId}-${i}`

  const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Escape') {
      if (query !== '') {
        e.preventDefault()
        setQuery('')
        setActive(0)
      }
    } else if (e.key === 'ArrowDown' && results.length > 0) {
      e.preventDefault()
      setActive((current + 1) % results.length)
    } else if (e.key === 'ArrowUp' && results.length > 0) {
      e.preventDefault()
      setActive((current - 1 + results.length) % results.length)
    } else if (e.key === 'Enter' && results.length > 0) {
      e.preventDefault()
      pick(results[current])
    }
  }

  return (
    <div className="relative mb-4 w-full max-w-md">
      <label htmlFor={inputId} className="block text-sm font-medium text-slate-700 dark:text-zinc-300 mb-1">
        {t('settings.search.label')}
      </label>
      <input
        ref={inputRef}
        id={inputId}
        type="search"
        role="combobox"
        aria-expanded={showList}
        aria-controls={listId}
        aria-autocomplete="list"
        aria-activedescendant={showList ? optionId(current) : undefined}
        autoComplete="off"
        value={query}
        placeholder={t('settings.search.placeholder')}
        onChange={e => { setQuery(e.target.value); setActive(0) }}
        onKeyDown={onKeyDown}
        onFocus={onActivate}
        className="w-full bg-white dark:bg-zinc-900 border border-slate-300 dark:border-zinc-700 rounded px-3 py-2 text-sm text-slate-900 dark:text-zinc-100 placeholder:text-slate-500 dark:placeholder:text-zinc-500 focus:outline-hidden focus-visible:ring-2 focus-visible:ring-emerald-500 -outline-offset-2"
      />
      <ul
        id={listId}
        role="listbox"
        aria-label={t('settings.search.results')}
        hidden={!showList}
        className="absolute z-20 left-0 right-0 mt-1 max-h-72 overflow-auto rounded border border-slate-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 shadow-lg"
      >
        {results.map((r, i) => (
          <li
            key={r.id}
            id={optionId(i)}
            role="option"
            aria-selected={i === current}
            onMouseDown={e => e.preventDefault()}
            onClick={() => pick(r)}
            className={`cursor-pointer px-3 py-2 text-sm ${
              i === current
                ? 'bg-slate-200 dark:bg-zinc-800 text-slate-900 dark:text-white'
                : 'text-slate-700 dark:text-zinc-300 hover:bg-slate-100 dark:hover:bg-zinc-800/50'
            }`}
          >
            <span className="font-medium break-words">
              {r.kind === 'tab' ? r.tabLabel : r.kind === 'heading' ? r.heading : r.settingKey}
            </span>
            {r.kind !== 'tab' && (
              <span className="ml-2 text-xs text-slate-600 dark:text-zinc-400">{r.tabLabel}</span>
            )}
          </li>
        ))}
      </ul>
      <p role="status" aria-live="polite" className="sr-only">
        {open ? (results.length === 0 ? t('settings.search.none') : t('settings.search.count', { count: results.length })) : ''}
      </p>
      {open && results.length === 0 && (
        <p className="mt-1 text-xs text-slate-600 dark:text-zinc-400" aria-hidden="true">{t('settings.search.none')}</p>
      )}
    </div>
  )
}
