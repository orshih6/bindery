import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api, type DBIntegrityReport } from '../../api/client'
import { slateFill } from '../../components/buttons'

const DOCS_URL = 'https://github.com/vavallee/bindery/blob/main/docs/DEPLOYMENT.md#database-foreign-key-integrity'

// DatabaseCheckSection is the read only foreign key drift check (#3356). The
// scan is a full pass over the database, so it only ever runs from the button:
// not on mount, not on a timer. Rendered for admins only by its parent.
export default function DatabaseCheckSection() {
  const { t } = useTranslation()
  const [running, setRunning] = useState(false)
  const [report, setReport] = useState<DBIntegrityReport | null>(null)
  const [error, setError] = useState('')

  const run = async () => {
    setRunning(true)
    setError('')
    try {
      setReport(await api.checkDBIntegrity())
    } catch (e) {
      setReport(null)
      setError(e instanceof Error && e.message ? e.message : t('settings.general.dbCheckFailedGeneric'))
    } finally {
      setRunning(false)
    }
  }

  const onDeleteLabel = (action: string) => {
    switch (action) {
      case 'CASCADE': return t('settings.general.dbCheckOnDeleteCascade')
      case 'SET NULL': return t('settings.general.dbCheckOnDeleteSetNull')
      case '': return t('settings.general.dbCheckOnDeleteUnknown')
      default: return t('settings.general.dbCheckOnDeleteOther')
    }
  }

  return (
    <section>
      <h3 className="text-base font-semibold mb-3 text-slate-800 dark:text-zinc-200">{t('settings.general.dbCheck')}</h3>
      <div className="p-4 border border-slate-200 dark:border-zinc-800 rounded-lg bg-slate-100 dark:bg-zinc-900 space-y-3">
        <div className="flex items-center justify-between gap-4">
          <p className="text-xs text-slate-600 dark:text-zinc-500">{t('settings.general.dbCheckHint')}</p>
          <button
            type="button"
            onClick={run}
            disabled={running}
            className={`px-4 py-2 ${slateFill} rounded text-sm font-medium disabled:opacity-50 flex-shrink-0`}
          >
            {running ? t('settings.general.dbCheckRunning') : t('settings.general.dbCheckButton')}
          </button>
        </div>

        {error && (
          <p role="alert" className="text-xs text-red-600 dark:text-red-400">
            {t('settings.general.dbCheckFailed', { message: error })}
          </p>
        )}

        {report && report.total === 0 && (
          <p role="status" className="text-xs text-emerald-700 dark:text-emerald-400">{t('settings.general.dbCheckClean')}</p>
        )}

        {report && report.total > 0 && (
          <div className="space-y-2">
            <p role="status" className="text-xs text-amber-600 dark:text-amber-400">
              {t('settings.general.dbCheckFound', { count: report.total })}
            </p>
            <table className="w-full text-xs text-left">
              <thead className="text-slate-600 dark:text-zinc-400">
                <tr>
                  <th className="py-1 pr-3 font-medium">{t('settings.general.dbCheckColRelationship')}</th>
                  <th className="py-1 pr-3 font-medium">{t('settings.general.dbCheckColRows')}</th>
                  <th className="py-1 font-medium">{t('settings.general.dbCheckColOnDelete')}</th>
                </tr>
              </thead>
              <tbody className="text-slate-800 dark:text-zinc-200">
                {report.relationships.map(r => (
                  <tr key={`${r.childTable}->${r.parentTable}`} className="border-t border-slate-200 dark:border-zinc-800">
                    <td className="py-1 pr-3 font-mono">{r.childTable} {'->'} {r.parentTable}</td>
                    <td className="py-1 pr-3 font-mono">{r.violations}</td>
                    <td className="py-1">{onDeleteLabel(r.onDelete)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        {report && (
          <p className="text-[11px] text-slate-500 dark:text-zinc-500">
            {t('settings.general.dbCheckCheckedAt', { time: new Date(report.checkedAt).toLocaleString() })}
          </p>
        )}

        <p className="text-xs text-slate-600 dark:text-zinc-500 border-t border-slate-200 dark:border-zinc-800 pt-3">
          {t('settings.general.dbCheckRepairNote')}{' '}
          <a href={DOCS_URL} target="_blank" rel="noopener noreferrer" className="text-emerald-700 dark:text-emerald-400 underline">
            {t('settings.general.dbCheckDocsLink')}
          </a>
        </p>
      </div>
    </section>
  )
}
