import React, { useCallback, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import "./styles.css";

const routes = [
  { id: "dashboard", label: "Overview" },
  { id: "users", label: "Users" },
  { id: "documents", label: "Documents" },
];

function getCookie(name) {
  const item = document.cookie.split("; ").find((value) => value.startsWith(`${name}=`));
  return item ? decodeURIComponent(item.split("=").slice(1).join("=")) : "";
}

function readRoute() {
  const value = window.location.hash.replace(/^#\/?/, "");
  return routes.some((route) => route.id === value) ? value : "dashboard";
}

function formatBytes(bytes) {
  if (!bytes) return "—";
  const units = ["B", "KB", "MB", "GB"];
  const power = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  return `${(bytes / 1024 ** power).toFixed(power ? 1 : 0)} ${units[power]}`;
}

function formatDate(value) {
  if (!value) return "—";
  return new Intl.DateTimeFormat(undefined, {
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(value));
}

function App() {
  const [route, setRoute] = useState(readRoute);
  const [session, setSession] = useState({ loading: true, user: null });
  const [csrf, setCSRF] = useState(() => getCookie("macocr_csrf"));
  const [toast, setToast] = useState("");

  const notify = useCallback((message) => {
    setToast(message);
    window.setTimeout(() => setToast(""), 3200);
  }, []);

  const api = useCallback(async (url, options = {}) => {
    const headers = { ...(options.headers || {}) };
    if (options.body && !headers["Content-Type"]) headers["Content-Type"] = "application/json";
    const token = csrf || getCookie("macocr_csrf");
    if (token) headers["X-CSRF-Token"] = token;
    const response = await fetch(url, { ...options, headers });
    if (response.status === 401 && url !== "/v1/auth/login") {
      setSession({ loading: false, user: null });
    }
    return response;
  }, [csrf]);

  useEffect(() => {
    const onHash = () => setRoute(readRoute());
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);

  useEffect(() => {
    api("/v1/auth/me")
      .then(async (response) => {
        if (!response.ok) throw new Error("signed out");
        setSession({ loading: false, user: await response.json() });
      })
      .catch(() => setSession({ loading: false, user: null }));
  }, [api]);

  async function login(email, password) {
    const response = await fetch("/v1/auth/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ email, password }),
    });
    const data = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(data.detail || "Invalid email or password");
    setCSRF(data.csrfToken || getCookie("macocr_csrf"));
    setSession({ loading: false, user: data.user });
    notify("Signed in");
  }

  async function logout() {
    await api("/v1/auth/logout", { method: "POST" });
    setCSRF("");
    setSession({ loading: false, user: null });
  }

  if (session.loading) return <LoadingScreen />;
  if (!session.user) return <Login onSubmit={login} />;

  return (
    <div className="shell">
      <aside className="sidebar">
        <a className="brand" href="#/dashboard" aria-label="OCR Admin home">
          <span className="brand-symbol">O</span>
          <span>OCR Admin</span>
        </a>
        <nav className="nav" aria-label="Admin navigation">
          {routes.map((item) => (
            <a key={item.id} href={`#/${item.id}`} className={route === item.id ? "active" : ""}>
              {item.label}
            </a>
          ))}
        </nav>
        <div className="sidebar-footer">
          <a href="/" target="_blank" rel="noreferrer">API docs <span>↗</span></a>
          <div className="account">
            <div>
              <span className="account-label">Signed in</span>
              <strong>{session.user.email}</strong>
            </div>
            <button className="text-button" onClick={logout}>Sign out</button>
          </div>
        </div>
      </aside>
      <main className="workspace">
        {route === "dashboard" && <Dashboard api={api} />}
        {route === "users" && <Users api={api} notify={notify} />}
        {route === "documents" && <Documents api={api} />}
      </main>
      {toast && <div className="toast" role="status">{toast}</div>}
    </div>
  );
}

function PageHeader({ eyebrow, title, description, action }) {
  return (
    <header className="page-header">
      <div>
        <span className="eyebrow">{eyebrow}</span>
        <h1>{title}</h1>
        <p>{description}</p>
      </div>
      {action}
    </header>
  );
}

function Dashboard({ api }) {
  const [data, setData] = useState(null);
  const [error, setError] = useState("");
  const load = useCallback(async () => {
    setError("");
    const response = await api("/v1/admin/dashboard");
    if (!response.ok) return setError("Could not load system metrics.");
    setData(await response.json());
  }, [api]);
  useEffect(() => { load(); }, [load]);

  const counts = data?.queueCounts || {};
  const stats = [
    ["Queued", counts.queued || 0, "Waiting for native OCR"],
    ["Processing", counts.processing || 0, "Active jobs"],
    ["Completed", counts.completed || 0, "Stored results"],
    ["Users", data?.totalUsers || 0, "Registered accounts"],
  ];
  return (
    <>
      <PageHeader eyebrow="System" title="Overview" description="A compact view of OCR traffic and account activity." action={<button className="secondary" onClick={load}>Refresh</button>} />
      {error && <Notice>{error}</Notice>}
      <section className="metric-grid">
        {stats.map(([label, value, hint]) => (
          <article className="metric" key={label}>
            <span>{label}</span><strong>{value}</strong><small>{hint}</small>
          </article>
        ))}
      </section>
      <section className="panel welcome-panel">
        <div>
          <span className="eyebrow">Operations</span>
          <h2>Keep access deliberate.</h2>
          <p>Create scoped API keys, set account quotas, and inspect processing state from one place.</p>
        </div>
        <a className="button" href="#/users">Manage users</a>
      </section>
    </>
  );
}

function Users({ api, notify }) {
  const [users, setUsers] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
  const [key, setKey] = useState("");

  const load = useCallback(async () => {
    setLoading(true); setError("");
    const response = await api("/v1/users?limit=100");
    if (!response.ok) { setError("Could not load users."); setLoading(false); return; }
    const data = await response.json();
    setUsers(data.users || []); setLoading(false);
  }, [api]);
  useEffect(() => { load(); }, [load]);

  async function createUser(values) {
    const response = await api("/v1/users", { method: "POST", body: JSON.stringify(values) });
    if (!response.ok) throw new Error((await response.json().catch(() => ({}))).detail || "Could not create user");
    setCreateOpen(false); notify("User created"); load();
  }

  async function createKey(user) {
    const name = window.prompt(`Name the API key for ${user.email}`, "production");
    if (!name) return;
    const rpm = user.config?.rate_limit_rpm || 60;
    const response = await api(`/v1/users/${user.id}/apikeys`, { method: "POST", body: JSON.stringify({ name, rate_limit_rpm: rpm }) });
    const data = await response.json().catch(() => ({}));
    if (!response.ok) return notify(data.detail || "Key creation failed");
    setKey(data.key);
  }

  async function resetQuota(user) {
    if (!window.confirm(`Reset document usage for ${user.email}?`)) return;
    const response = await api(`/v1/users/${user.id}/config/reset-quota`, { method: "POST" });
    if (response.ok) { notify("Quota reset"); load(); }
  }

  async function deactivate(user) {
    if (!window.confirm(`Deactivate ${user.email}? New authenticated requests will be blocked.`)) return;
    const response = await api(`/v1/users/${user.id}/deactivate`, { method: "POST" });
    if (response.ok) { notify("User deactivated"); load(); }
  }

  return (
    <>
      <PageHeader eyebrow="Access" title="Users" description="Accounts, request rates, document and aggregate storage quotas, and API credentials." action={<button onClick={() => setCreateOpen(true)}>New user</button>} />
      {error && <Notice>{error}</Notice>}
      <section className="panel table-panel">
        <div className="table-meta"><span>{users.length} accounts</span><button className="text-button" onClick={load}>Refresh</button></div>
        <div className="table-scroll">
          <table>
            <thead><tr><th>User</th><th>Status</th><th>Rate</th><th>Documents</th><th>Storage quota</th><th>Stored</th><th>Reserved</th><th><span className="sr-only">Actions</span></th></tr></thead>
            <tbody>
              {loading && <EmptyRow columns={8}>Loading accounts…</EmptyRow>}
              {!loading && users.length === 0 && <EmptyRow columns={8}>No users yet.</EmptyRow>}
              {!loading && users.map((user) => (
                <tr key={user.id}>
                  <td><div className="primary-cell"><strong>{user.email}</strong><small>#{user.id} · {user.role}</small></div></td>
                  <td><Badge status={user.disabled ? "disabled" : "active"} /></td>
                  <td>{user.config?.rate_limit_rpm ?? 60} rpm</td>
                  <td>{(user.config?.doc_used || 0).toLocaleString()} / {user.config?.doc_quota ? user.config.doc_quota.toLocaleString() : "∞"}</td>
                  <td>{user.config?.storage_quota_bytes ? formatBytes(user.config.storage_quota_bytes) : "Unlimited"}</td>
                  <td>{formatBytes(user.config?.storage_used_bytes || 0)}</td>
                  <td>{formatBytes(user.config?.storage_reserved_bytes || 0)}</td>
                  <td><div className="row-actions"><button className="small secondary" onClick={() => createKey(user)}>Create key</button><button className="small ghost" onClick={() => resetQuota(user)}>Reset</button>{!user.disabled && <button className="small ghost danger" onClick={() => deactivate(user)}>Deactivate</button>}</div></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
      {createOpen && <CreateUserModal onClose={() => setCreateOpen(false)} onSubmit={createUser} />}
      {key && <KeyModal value={key} onClose={() => setKey("")} notify={notify} />}
    </>
  );
}

function Documents({ api }) {
  const [documents, setDocuments] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const load = useCallback(async () => {
    setLoading(true); setError("");
    const response = await api("/v1/admin/documents?limit=100");
    if (!response.ok) { setError("Could not load documents."); setLoading(false); return; }
    const data = await response.json();
    setDocuments(data.documents || []); setLoading(false);
  }, [api]);
  useEffect(() => { load(); }, [load]);

  return (
    <>
      <PageHeader eyebrow="Operations" title="Documents" description="Admin-only visibility into recent OCR jobs before retention cleanup." action={<button className="secondary" onClick={load}>Refresh</button>} />
      {error && <Notice>{error}</Notice>}
      <section className="panel table-panel">
        <div className="table-meta"><span>{documents.length} recent documents</span><small>Records expire automatically</small></div>
        <div className="table-scroll">
          <table>
            <thead><tr><th>Document</th><th>User</th><th>Status</th><th>Input</th><th>Created</th><th>Result</th></tr></thead>
            <tbody>
              {loading && <EmptyRow columns={6}>Loading documents…</EmptyRow>}
              {!loading && documents.length === 0 && <EmptyRow columns={6}>No retained documents.</EmptyRow>}
              {!loading && documents.map((document) => (
                <tr key={document.id}>
                  <td><code className="document-id" title={document.id}>{document.id}</code></td>
                  <td>#{document.user_id}</td>
                  <td><Badge status={document.status} /></td>
                  <td><div className="primary-cell"><span>{document.input_content_type || "Unknown"}</span><small>{formatBytes(document.input_size_bytes)}</small></div></td>
                  <td>{formatDate(document.created_at)}</td>
                  <td className="result-preview">{document.result_text || "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
    </>
  );
}

function Login({ onSubmit }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  async function submit(event) {
    event.preventDefault(); setBusy(true); setError("");
    try { await onSubmit(email, password); } catch (err) { setError(err.message); setBusy(false); }
  }
  return (
    <main className="login-page">
      <section className="login-card">
        <div className="brand login-brand"><span className="brand-symbol">O</span><span>OCR Admin</span></div>
        <div className="login-copy"><span className="eyebrow">Restricted access</span><h1>Sign in</h1><p>Manage users, credentials, quotas, and OCR operations.</p></div>
        <form onSubmit={submit}>
          <label>Email<input autoFocus type="email" value={email} onChange={(event) => setEmail(event.target.value)} placeholder="admin@example.com" required /></label>
          <label>Password<input type="password" value={password} onChange={(event) => setPassword(event.target.value)} placeholder="Your password" required /></label>
          {error && <p className="form-error">{error}</p>}
          <button disabled={busy} type="submit">{busy ? "Signing in…" : "Sign in"}</button>
        </form>
      </section>
      <p className="login-footnote">OCR Platform · Internal administration</p>
    </main>
  );
}

function CreateUserModal({ onClose, onSubmit }) {
  const [email, setEmail] = useState("");
  const [rate, setRate] = useState(60);
  const [quota, setQuota] = useState(1000);
  const [storageGB, setStorageGB] = useState(10);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  async function submit(event) {
    event.preventDefault(); setBusy(true); setError("");
    try { await onSubmit({ email, rate_limit_rpm: Number(rate), doc_quota: Number(quota), storage_quota_bytes: Number(storageGB) * 1024 * 1024 * 1024 }); } catch (err) { setError(err.message); setBusy(false); }
  }
  return <Modal title="Create user" description="Set account-level limits now; API keys can be created after." onClose={onClose}>
    <form onSubmit={submit} className="modal-form">
      <label>Email<input autoFocus type="email" value={email} onChange={(event) => setEmail(event.target.value)} placeholder="user@example.com" required /></label>
      <div className="field-grid"><label>Requests / minute<input type="number" min="0" value={rate} onChange={(event) => setRate(event.target.value)} required /></label><label>Document quota<input type="number" min="0" value={quota} onChange={(event) => setQuota(event.target.value)} required /></label><label>Storage quota (GiB)<input type="number" min="0" value={storageGB} onChange={(event) => setStorageGB(event.target.value)} required /></label></div>
      <small className="field-note">A quota of 0 means unlimited. Storage includes retained inputs and unconsumed presigned reservations.</small>
      {error && <p className="form-error">{error}</p>}
      <div className="modal-actions"><button type="button" className="secondary" onClick={onClose}>Cancel</button><button disabled={busy} type="submit">{busy ? "Creating…" : "Create user"}</button></div>
    </form>
  </Modal>;
}

function KeyModal({ value, onClose, notify }) {
  async function copy() {
    await navigator.clipboard.writeText(value); notify("API key copied");
  }
  return <Modal title="API key created" description="Copy it now. The full key will not be shown again." onClose={onClose}>
    <div className="key-value">{value}</div>
    <div className="modal-actions"><button className="secondary" onClick={copy}>Copy key</button><button onClick={onClose}>I saved it</button></div>
  </Modal>;
}

function Modal({ title, description, onClose, children }) {
  useEffect(() => {
    const onKey = (event) => event.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  return <div className="modal-backdrop" role="presentation" onMouseDown={(event) => event.target === event.currentTarget && onClose()}><section className="modal" role="dialog" aria-modal="true" aria-labelledby="modal-title"><button className="modal-close" onClick={onClose} aria-label="Close">×</button><h2 id="modal-title">{title}</h2><p>{description}</p>{children}</section></div>;
}

function Badge({ status }) { return <span className={`badge status-${status}`}>{status}</span>; }
function EmptyRow({ columns, children }) { return <tr><td className="empty" colSpan={columns}>{children}</td></tr>; }
function Notice({ children }) { return <div className="notice" role="alert">{children}</div>; }
function LoadingScreen() { return <main className="loading-screen"><span className="brand-symbol">O</span><span>Loading admin…</span></main>; }

createRoot(document.getElementById("root")).render(<React.StrictMode><App /></React.StrictMode>);
