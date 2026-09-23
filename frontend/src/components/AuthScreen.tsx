import { useState, type FormEvent } from 'react'
import { ArrowRight, KeyRound, LoaderCircle, Mail, ShieldCheck } from 'lucide-react'
import { authAPI, type AuthSession } from '../api'

type AuthMode = 'login' | 'register' | 'reset'

export function AuthScreen({ onAuthenticated }: { onAuthenticated: (session: AuthSession) => void }) {
  const [mode, setMode] = useState<AuthMode>('login')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [code, setCode] = useState('')
  const [challengeId, setChallengeId] = useState('')
  const [busy, setBusy] = useState(false)
  const [codeBusy, setCodeBusy] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')

  const changeMode = (next: AuthMode) => {
    setMode(next)
    setPassword('')
    setCode('')
    setChallengeId('')
    setError('')
    setMessage('')
  }

  const requestCode = async () => {
    if (!email.trim() || codeBusy) return
    setCodeBusy(true)
    setError('')
    setMessage('')
    try {
      const challenge = await authAPI.requestVerification(email.trim(), mode === 'reset' ? 'reset' : 'register')
      setChallengeId(challenge.challenge_id)
      setMessage('验证码已发送，请在十分钟内完成验证。')
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : '验证码发送失败')
    } finally {
      setCodeBusy(false)
    }
  }

  const readDevCode = async () => {
    setCodeBusy(true)
    setError('')
    try {
      const messages = await authAPI.devOutbox(email.trim())
      const latest = messages.find((item) => item.purpose === (mode === 'reset' ? 'reset' : 'register'))
      if (!latest) throw new Error('开发收件箱里还没有这次验证码')
      setCode(latest.code)
      setMessage('已从本地开发收件箱读取验证码。')
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : '读取开发验证码失败')
    } finally {
      setCodeBusy(false)
    }
  }

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    setBusy(true)
    setError('')
    setMessage('')
    try {
      if (mode === 'login') {
        onAuthenticated(await authAPI.login(email.trim(), password))
        return
      }
      if (!challengeId) {
        setError('请先获取邮箱验证码')
        return
      }
      if (mode === 'register') {
        onAuthenticated(await authAPI.register(challengeId, code.trim(), password))
        return
      }
      await authAPI.resetPassword(challengeId, code.trim(), password)
      setMode('login')
      setChallengeId('')
      setCode('')
      setPassword('')
      setMessage('密码已经重置，请使用新密码登录。')
    } catch (submitError) {
      setError(submitError instanceof Error ? submitError.message : '操作失败，请稍后重试')
    } finally {
      setBusy(false)
    }
  }

  return (
    <main className="auth-page">
      <section className="auth-intro">
        <div className="auth-brand"><span className="brand-mark">J</span><strong>JobPilot</strong></div>
        <div className="auth-intro-copy">
          <span className="auth-eyebrow">求职项目与能力规划</span>
          <h1>从真实岗位需求出发，找到值得写进简历的项目。</h1>
          <p>收集目标岗位 JD，建立市场画像与个人能力画像，再生成项目方向和知识补强建议。</p>
        </div>
        <div className="auth-security-note"><ShieldCheck size={18} /><span>账号由 JobPilot 与 StudyFlow 共用认证服务统一保护。</span></div>
      </section>

      <section className="auth-panel" aria-labelledby="auth-title">
        <div className="auth-card">
          <div className="auth-card-heading">
            <span className="auth-card-icon">{mode === 'login' ? <KeyRound size={20} /> : <Mail size={20} />}</span>
            <div>
              <h2 id="auth-title">{mode === 'login' ? '登录 JobPilot' : mode === 'register' ? '创建账号' : '重置密码'}</h2>
              <p>{mode === 'login' ? '继续完善你的求职画像。' : '当前账号系统支持邮箱验证。'}</p>
            </div>
          </div>

          <form className="auth-form" onSubmit={submit}>
            <label>邮箱
              <input type="email" value={email} onChange={(event) => setEmail(event.target.value)} required autoComplete="email" disabled={Boolean(challengeId)} placeholder="name@example.com" />
            </label>

            {mode !== 'login' && (
              <div className="auth-verification-row">
                <label>验证码
                  <input value={code} onChange={(event) => setCode(event.target.value.replace(/\D/g, '').slice(0, 6))} required maxLength={6} inputMode="numeric" autoComplete="one-time-code" placeholder="六位验证码" />
                </label>
                <button className="button" type="button" onClick={requestCode} disabled={codeBusy || !email.trim() || Boolean(challengeId)}>
                  {codeBusy ? '处理中……' : challengeId ? '已发送' : '获取验证码'}
                </button>
              </div>
            )}

            <label>{mode === 'reset' ? '新密码' : '密码'}
              <input type="password" value={password} onChange={(event) => setPassword(event.target.value)} required minLength={8} maxLength={72} autoComplete={mode === 'login' ? 'current-password' : 'new-password'} placeholder="8～72 个字符" />
            </label>

            {import.meta.env.DEV && mode !== 'login' && challengeId && (
              <button className="auth-dev-code" type="button" onClick={readDevCode} disabled={codeBusy}>读取本地开发验证码</button>
            )}

            {message && <p className="auth-message" role="status">{message}</p>}
            {error && <p className="auth-error" role="alert">{error}</p>}

            <button className="button button-primary auth-submit" type="submit" disabled={busy}>
              {busy && <LoaderCircle className="spin" size={16} />}
              {mode === 'login' ? '登录' : mode === 'register' ? '注册并进入' : '确认重置'}
              {!busy && <ArrowRight size={16} />}
            </button>
          </form>

          <div className="auth-switches">
            {mode !== 'login' && <button type="button" onClick={() => changeMode('login')}>返回登录</button>}
            {mode === 'login' && <button type="button" onClick={() => changeMode('register')}>注册账号</button>}
            {mode === 'login' && <button type="button" onClick={() => changeMode('reset')}>忘记密码</button>}
          </div>
        </div>
      </section>
    </main>
  )
}
