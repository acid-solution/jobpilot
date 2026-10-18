import { useRef, useState, type FormEvent } from 'react'
import { ShieldCheck, Trash2 } from 'lucide-react'
import { authAPI } from '../api'

export function AccountSettings({ email, onDeleted }: { email: string; onDeleted: () => void }) {
  const [confirming, setConfirming] = useState(false)
  const [password, setPassword] = useState('')
  const [acknowledged, setAcknowledged] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const inFlight = useRef(false)

  const cancel = () => {
    setConfirming(false)
    setPassword('')
    setAcknowledged(false)
    setError('')
  }
  const remove = async (event: FormEvent) => {
    event.preventDefault()
    if (inFlight.current || !password || !acknowledged) return
    inFlight.current = true
    setBusy(true)
    setError('')
    try {
      await authAPI.deleteAccount(password)
      setPassword('')
      onDeleted()
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '注销失败，请稍后重试')
    } finally {
      inFlight.current = false
      setBusy(false)
    }
  }

  return <section className="settings-card account-deletion" aria-labelledby="account-security-title">
    <div className="settings-card-head">
      <span className="settings-provider-icon"><ShieldCheck size={20} /></span>
      <div><h2 id="account-security-title">账号安全</h2><p>{email}</p></div>
    </div>
    <div className="account-deletion-body">
      <h3>注销共享账号</h3>
      <p>注销后，当前邮箱在 JobPilot 和 StudyFlow 的共享账号将永久删除，两个产品的全部刷新会话都会失效，此操作无法撤销。</p>
      <p>已有求职资料、学习计划等业务历史暂不自动清除。重新注册会创建新账号，无法访问原账号的数据。</p>
      <p>其他已打开页面的登录凭证最长可能在 15 分钟后失效。</p>
      {!confirming ? <button className="button button-danger" type="button" onClick={() => setConfirming(true)}><Trash2 size={15} />注销账号</button> :
        <form className="account-deletion-form" onSubmit={remove} aria-label="确认注销共享账号" aria-busy={busy}>
          <label htmlFor="delete-account-password">当前密码</label>
          <input id="delete-account-password" type="password" autoComplete="current-password" autoFocus required disabled={busy} value={password} onChange={event => setPassword(event.target.value)} />
          <label className="account-deletion-check"><input type="checkbox" required disabled={busy} checked={acknowledged} onChange={event => setAcknowledged(event.target.checked)} />我确认同时注销 JobPilot 和 StudyFlow 的共享账号，并理解此操作无法撤销。</label>
          {error && <p className="settings-error" role="alert">{error}</p>}
          <div className="account-deletion-actions"><button className="button" type="button" disabled={busy} onClick={cancel}>取消</button><button className="button button-danger" type="submit" disabled={busy || !password || !acknowledged}>{busy ? '正在注销…' : '永久注销账号'}</button></div>
        </form>}
    </div>
  </section>
}
