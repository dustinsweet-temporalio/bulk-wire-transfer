// FI-branded sign-in: Company ID, User ID, Password. DEMO-ONLY: a collapsible hint lists this FI's demo users
// (password "demo"). PRODUCTION NOTE: a real portal signs in through the FI's identity provider.
import { useState, type FormEvent } from "react";
import { useNavigate } from "react-router";
import { api } from "../api";
import { fiId, useBranding, Wordmark } from "../branding";
import { session } from "../session";

export function Login() {
  const branding = useBranding();
  const navigate = useNavigate();
  const [companyId, setCompanyId] = useState("");
  const [userId, setUserId] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const { token, user } = await api.login(fiId(), companyId, userId, password);
      session.signIn(token, user);
      navigate(user.role === "BankOps" ? "/ops/exceptions" : "/");
    } catch {
      setError("We couldn't sign you in. Check your Company ID, User ID, and password.");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="login-page">
      <div className="login-band">
        <Wordmark branding={branding} size={36} />
      </div>
      <div className="login-body">
        <div className="card login-card">
          <h1 style={{ marginBottom: 4 }}>Business Online Banking</h1>
          <p className="secondary" style={{ marginTop: 0 }}>{branding.displayName}</p>
          <form onSubmit={submit}>
            <label className="field">Company ID<input value={companyId} onChange={(e) => setCompanyId(e.target.value)} autoComplete="organization" required /></label>
            <label className="field">User ID<input value={userId} onChange={(e) => setUserId(e.target.value)} autoComplete="username" required /></label>
            <label className="field">Password<input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" required /></label>
            {error && <div className="banner danger" role="alert">{error}</div>}
            <button className="btn primary" type="submit" disabled={busy}>{busy ? "Signing in…" : "Sign in"}</button>
          </form>
          <details className="hint" style={{ marginTop: 18 }}>
            <summary>Demo users</summary>
            <p>All demo users share the password <strong>demo</strong>. Bank staff use Company ID <strong>BANK</strong>.</p>
            <table>
              <thead><tr><th>Company ID</th><th>User ID</th><th>Role</th></tr></thead>
              <tbody>
                {branding.demoUsers.map((u) => (
                  <tr key={u.userId}>
                    <td>{u.companyId}</td>
                    <td><button type="button" className="link" onClick={() => { setCompanyId(u.companyId); setUserId(u.userId); setPassword("demo"); }}>{u.userId}</button></td>
                    <td>{u.name} · {u.role}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </details>
        </div>
      </div>
    </div>
  );
}
