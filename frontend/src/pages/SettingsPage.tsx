import { useEffect, useState } from 'react'
import { CheckCircle2, KeyRound, LoaderCircle, ShieldCheck, Trash2 } from 'lucide-react'
import { jobPilotAPI, type ModelConfig } from '../api'

export function SettingsPage() {
  const [config, setConfig] = useState<ModelConfig | null>(null)
  const [apiKey, setAPIKey] = useState('')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')

  useEffect(() => {
    jobPilotAPI.getDeepSeekConfig()
      .then(setConfig)
      .catch((loadError: unknown) => setError(loadError instanceof Error ? loadError.message : '读取模型配置失败'))
      .finally(() => setLoading(false))
  }, [])

  const save = async () => {
    if (!apiKey.trim()) return
    setSaving(true)
    setError('')
    setMessage('')
    try {
      const saved = await jobPilotAPI.saveDeepSeekConfig(apiKey.trim())
      setConfig(saved)
      setAPIKey('')
      setMessage('DeepSeek API Key 已加密保存。')
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  const test = async () => {
    setTesting(true)
    setError('')
    setMessage('')
    try {
      await jobPilotAPI.testDeepSeekConfig(apiKey.trim())
      setMessage('DeepSeek 连接与 JSON 输出测试通过。')
    } catch (testError) {
      setError(testError instanceof Error ? testError.message : '连接测试失败')
    } finally {
      setTesting(false)
    }
  }

  const remove = async () => {
    setDeleting(true)
    setError('')
    setMessage('')
    try {
      await jobPilotAPI.deleteDeepSeekConfig()
      setConfig({ provider: 'deepseek', model: 'deepseek-flash', configured: false })
      setAPIKey('')
      setMessage('DeepSeek API Key 已删除。')
    } catch (deleteError) {
      setError(deleteError instanceof Error ? deleteError.message : '删除失败')
    } finally {
      setDeleting(false)
    }
  }

  return (
    <div className="page-content settings-page">
      <div className="page-heading-row">
        <div>
          <h1>设置</h1>
          <p className="page-description">配置个人分析使用的模型服务。当前先支持 DeepSeek。</p>
        </div>
      </div>

      <section className="settings-card">
        <div className="settings-card-head">
          <span className="settings-provider-icon"><KeyRound size={20} /></span>
          <div><h2>DeepSeek</h2><p>用于 JD 基础解析，费用由你的 DeepSeek 账号承担。</p></div>
          <span className={config?.configured ? 'config-state is-ready' : 'config-state'}>
            {loading ? '读取中' : config?.configured ? '已配置' : '未配置'}
          </span>
        </div>

        <div className="settings-fields">
          <label>模型
            <select value="deepseek-flash" disabled><option value="deepseek-flash">deepseek-flash</option></select>
          </label>
          <label>API Key
            <input
              type="password"
              value={apiKey}
              onChange={(event) => setAPIKey(event.target.value)}
              placeholder={config?.configured ? `已保存 ${config.key_hint ?? ''}，输入新 Key 可替换` : '请输入 DeepSeek API Key'}
              autoComplete="off"
            />
          </label>
        </div>

        <div className="credential-note"><ShieldCheck size={17} /><span>Key 使用服务端加密密钥保存，页面和接口不会返回完整内容。</span></div>
        {message && <div className="settings-message" role="status"><CheckCircle2 size={16} />{message}</div>}
        {error && <div className="settings-error" role="alert">{error}</div>}

        <div className="settings-actions">
          {config?.configured && <button className="button button-danger" type="button" onClick={remove} disabled={deleting}><Trash2 size={15} />{deleting ? '删除中……' : '删除 Key'}</button>}
          <span />
          <button className="button" type="button" onClick={test} disabled={testing || (!apiKey.trim() && !config?.configured)}>
            {testing && <LoaderCircle className="spin" size={15} />}{testing ? '测试中……' : '测试连接'}
          </button>
          <button className="button button-primary" type="button" onClick={save} disabled={saving || !apiKey.trim()}>
            {saving ? '保存中……' : config?.configured ? '替换 Key' : '保存 Key'}
          </button>
        </div>
      </section>
    </div>
  )
}
