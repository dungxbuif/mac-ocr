import React, { useState, useEffect, useCallback } from "react";
import { createRoot } from "react-dom/client";
import {
  Users as UsersIcon,
  Key,
  Plus,
  RefreshCw,
  LogOut,
  ExternalLink,
  Shield,
  Trash2,
  Copy,
  Check,
  X,
  Sliders,
  ArrowLeft,
  Activity,
  KeyRound,
  Eye,
  EyeOff,
  UserCheck,
  UserX,
  Infinity as InfinityIcon
} from "lucide-react";
import "./styles.css";

interface UserConfig {
  rate_limit_rpm?: number;
  doc_quota?: number;
  doc_used?: number;
  storage_quota_bytes?: number;
  storage_used_bytes?: number;
  storage_reserved_bytes?: number;
}

interface User {
  id: number;
  email: string;
  role: "admin" | "user";
  disabled: boolean;
  config?: UserConfig;
}

interface ApiKey {
  id: number;
  name: string;
  prefix: string;
  rate_limit_rpm: number;
  created_at: string;
  revoked_at?: string | null;
}

function getCookie(name: string): string {
  const match = document.cookie.split("; ").find((row) => row.startsWith(`${name}=`));
  return match ? decodeURIComponent(match.split("=").slice(1).join("=")) : "";
}

function formatBytes(bytes?: number): string {
  if (!bytes) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  return `${(bytes / Math.pow(1024, i)).toFixed(i ? 1 : 0)} ${units[i]}`;
}

function formatDate(iso?: string): string {
  if (!iso) return "—";
  return new Intl.DateTimeFormat(undefined, {
    month: "short",
    day: "2-digit",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(iso));
}

function generateStrongPassword(): string {
  const chars = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789!@#$%^&*";
  let pass = "";
  const cryptoObj = window.crypto || (window as any).msCrypto;
  const values = new Uint32Array(16);
  cryptoObj.getRandomValues(values);
  for (let i = 0; i < 16; i++) {
    pass += chars[values[i] % chars.length];
  }
  return pass;
}

function App() {
  const [session, setSession] = useState<{ loading: boolean; user: User | null }>({ loading: true, user: null });
  const [selectedUser, setSelectedUser] = useState<User | null>(null);
  const [csrf, setCsrf] = useState(() => getCookie("macocr_csrf"));
  const [toast, setToast] = useState("");

  const notify = useCallback((message: string) => {
    setToast(message);
    window.setTimeout(() => setToast(""), 3500);
  }, []);

  const api = useCallback(
    async (path: string, options: RequestInit = {}) => {
      const headers: Record<string, string> = { ...((options.headers as Record<string, string>) || {}) };
      if (options.body && !headers["Content-Type"]) {
        headers["Content-Type"] = "application/json";
      }
      const token = csrf || getCookie("macocr_csrf");
      if (token) {
        headers["X-CSRF-Token"] = token;
      }
      const response = await fetch(path, { ...options, headers });
      if (response.status === 401 && path !== "/v1/auth/login") {
        setSession({ loading: false, user: null });
      }
      return response;
    },
    [csrf]
  );

  useEffect(() => {
    api("/v1/auth/me")
      .then(async (response) => {
        if (!response.ok) throw new Error("signed out");
        setSession({ loading: false, user: await response.json() });
      })
      .catch(() => setSession({ loading: false, user: null }));
  }, [api]);

  async function login(email: string, pass: string) {
    const response = await fetch("/v1/auth/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ email, password: pass }),
    });
    const data = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(data.detail || "Invalid email or password");
    setCsrf(data.csrfToken || getCookie("macocr_csrf"));
    setSession({ loading: false, user: data.user });
    notify("Signed in successfully");
  }

  async function logout() {
    await api("/v1/auth/logout", { method: "POST" });
    setCsrf("");
    setSession({ loading: false, user: null });
  }

  if (session.loading) {
    return (
      <div style={{ minHeight: "100vh", display: "flex", alignItems: "center", justifyContent: "center", background: "#f8fafc" }}>
        <Shield size={28} color="#2563eb" />
      </div>
    );
  }

  if (!session.user) return <Login onSubmit={login} />;

  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="brand">
          <span className="brand-symbol">
            <Shield size={18} />
          </span>
          <div>
            <div style={{ lineHeight: 1.2, fontWeight: 700 }}>MacOCR</div>
            <span style={{ fontSize: "11px", color: "var(--text-dim)", fontWeight: 500 }}>Cluster Control Plane</span>
          </div>
        </div>
        <nav className="nav">
          <a
            href="#/users"
            onClick={(e) => {
              e.preventDefault();
              setSelectedUser(null);
            }}
            className={`nav-link ${!selectedUser ? "active" : ""}`}
          >
            <UsersIcon size={16} />
            <span>Accounts & API Keys</span>
          </a>
        </nav>
        <div className="sidebar-footer">
          <a href="/" target="_blank" rel="noreferrer" className="nav-link" style={{ fontSize: "12px" }}>
            <span>API Docs</span>
            <ExternalLink size={13} style={{ marginLeft: "auto" }} />
          </a>
          <div className="account-box">
            <div style={{ overflow: "hidden", textOverflow: "ellipsis" }}>
              <div style={{ fontSize: "10px", textTransform: "uppercase", color: "var(--text-dim)", fontWeight: 600 }}>System Admin</div>
              <strong style={{ fontSize: "12px", whiteSpace: "nowrap", color: "var(--text-main)" }}>{session.user.email}</strong>
            </div>
            <button className="btn btn-ghost btn-sm" onClick={logout} title="Sign out">
              <LogOut size={14} />
            </button>
          </div>
        </div>
      </aside>

      <main className="workspace">
        {selectedUser ? (
          <UserApiKeysPage
            user={selectedUser}
            api={api}
            notify={notify}
            onBack={() => setSelectedUser(null)}
            onUserUpdated={(u) => setSelectedUser(u)}
          />
        ) : (
          <UsersListPage
            api={api}
            notify={notify}
            currentUser={session.user}
            onSelectUser={(u) => setSelectedUser(u)}
          />
        )}
      </main>
      {toast && <div className="toast">{toast}</div>}
    </div>
  );
}

function UsersListPage({
  api,
  notify,
  currentUser,
  onSelectUser,
}: {
  api: (p: string, opt?: RequestInit) => Promise<Response>;
  notify: (m: string) => void;
  currentUser: User;
  onSelectUser: (u: User) => void;
}) {
  const [users, setUsers] = useState<User[]>([]);
  const [loading, setLoading] = useState(true);
  const [createOpen, setCreateOpen] = useState(false);
  const [editLimitsUser, setEditLimitsUser] = useState<User | null>(null);
  const [resetPassUser, setResetPassUser] = useState<User | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    const res = await api("/v1/users?limit=100");
    if (res.ok) {
      const d = await res.json();
      setUsers(d.users || []);
    }
    setLoading(false);
  }, [api]);

  useEffect(() => { load(); }, [load]);

  async function handleCreateUser(vals: any) {
    const res = await api("/v1/users", { method: "POST", body: JSON.stringify(vals) });
    if (!res.ok) {
      const d = await res.json().catch(() => ({}));
      throw new Error(d.detail || "Could not create account");
    }
    setCreateOpen(false);
    notify("User account created successfully");
    load();
  }

  async function handleSaveLimits(userId: number, rate: number, quota: number, storageGB: number) {
    const res = await api(`/v1/users/${userId}/config`, {
      method: "PUT",
      body: JSON.stringify({
        rate_limit_rpm: rate,
        doc_quota: quota,
        storage_quota_bytes: storageGB * 1024 * 1024 * 1024,
      }),
    });
    if (res.ok) {
      setEditLimitsUser(null);
      notify("Quotas and limits updated");
      load();
    } else {
      notify("Failed to update limits");
    }
  }

  async function handleResetPassword(userId: number, pass: string) {
    const res = await api(`/v1/users/${userId}/reset-password`, {
      method: "POST",
      body: JSON.stringify({ password: pass }),
    });
    if (res.ok) {
      setResetPassUser(null);
      notify("Password reset successfully");
    } else {
      const d = await res.json().catch(() => ({}));
      throw new Error(d.detail || "Failed to reset password");
    }
  }

  async function toggleStatus(u: User) {
    if (u.id === currentUser.id) return notify("Cannot deactivate primary administrator");
    const action = u.disabled ? "reactivate" : "deactivate";
    const confirmMsg = u.disabled
      ? `Reactivate account ${u.email}? API keys will immediately become usable again.`
      : `Suspend account ${u.email}? All API requests using this account's keys will be blocked immediately.`;
    if (!window.confirm(confirmMsg)) return;

    const res = await api(`/v1/users/${u.id}/${action}`, { method: "POST" });
    if (res.ok) {
      notify(u.disabled ? "Account reactivated" : "Account suspended");
      load();
    } else {
      notify(`Failed to ${action} user`);
    }
  }

  return (
    <>
      <div className="page-header">
        <div>
          <div className="eyebrow">
            <Activity size={12} />
            <span>Developer Management</span>
          </div>
          <h1>User Accounts & Quotas</h1>
          <p className="page-desc">Provision user accounts, configure rate & storage quotas, manage passwords, and inspect issued API keys.</p>
        </div>
        <button className="btn btn-primary" onClick={() => setCreateOpen(true)}>
          <Plus size={15} />
          <span>Create User</span>
        </button>
      </div>

      <div className="panel">
        <div className="panel-header">
          <span style={{ fontSize: "13px", fontWeight: 650 }}>{users.length} User Accounts</span>
          <button className="btn btn-ghost btn-sm" onClick={load}>
            <RefreshCw size={13} />
            <span>Refresh</span>
          </button>
        </div>
        <table className="data-table">
          <thead>
            <tr>
              <th>Account</th>
              <th>Status</th>
              <th>Rate Limit</th>
              <th>Document Quota</th>
              <th>Storage Quota</th>
              <th>Storage Used</th>
              <th style={{ textAlign: "right" }}>Actions</th>
            </tr>
          </thead>
          <tbody>
            {loading && (
              <tr>
                <td colSpan={7} style={{ textAlign: "center", padding: "32px", color: "var(--text-dim)" }}>
                  Loading accounts…
                </td>
              </tr>
            )}
            {!loading &&
              users.map((u) => {
                const isAdmin = u.role === "admin";
                const isUnlimitedRate = !u.config?.rate_limit_rpm || u.config.rate_limit_rpm === 0;
                const isUnlimitedDoc = !u.config?.doc_quota || u.config.doc_quota === 0;
                const isUnlimitedStorage = !u.config?.storage_quota_bytes || u.config.storage_quota_bytes === 0;

                return (
                  <tr key={u.id}>
                    <td>
                      <div>
                        <strong style={{ color: "var(--text-main)", display: "block" }}>{u.email}</strong>
                        <span style={{ fontSize: "11px", color: "var(--text-dim)" }}>
                          ID #{u.id} · {isAdmin ? "SYSTEM ADMINISTRATOR" : "STANDARD TENANT"}
                        </span>
                      </div>
                    </td>
                    <td>
                      <span className={`badge ${u.disabled ? "badge-disabled" : "badge-active"}`}>
                        {u.disabled ? "Suspended" : "Active"}
                      </span>
                    </td>
                    <td>
                      {isAdmin || isUnlimitedRate ? (
                        <span className="code-badge" style={{ color: "#7c3aed", background: "#f5f3ff", borderColor: "#ddd6fe" }}>
                          ∞ Unlimited
                        </span>
                      ) : (
                        <span className="code-badge">{u.config.rate_limit_rpm} RPM</span>
                      )}
                    </td>
                    <td>
                      {isAdmin || isUnlimitedDoc ? (
                        <span>{(u.config?.doc_used || 0).toLocaleString()} / ∞</span>
                      ) : (
                        <span>{(u.config?.doc_used || 0).toLocaleString()} / {u.config.doc_quota.toLocaleString()}</span>
                      )}
                    </td>
                    <td>
                      {isAdmin || isUnlimitedStorage ? (
                        <span>Unlimited</span>
                      ) : (
                        <span>{formatBytes(u.config.storage_quota_bytes)}</span>
                      )}
                    </td>
                    <td>{formatBytes(u.config?.storage_used_bytes || 0)}</td>
                    <td style={{ textAlign: "right" }}>
                      <div style={{ display: "inline-flex", gap: "6px" }}>
                        <button className="btn btn-primary btn-sm" onClick={() => onSelectUser(u)} title="View and manage API keys">
                          <Key size={13} />
                          <span>API Keys</span>
                        </button>
                        {!isAdmin && (
                          <>
                            <button className="btn btn-secondary btn-sm" onClick={() => setEditLimitsUser(u)} title="Configure Rate Limits & Quotas">
                              <Sliders size={13} />
                              <span>Limits</span>
                            </button>
                            <button className="btn btn-secondary btn-sm" onClick={() => setResetPassUser(u)} title="Reset User Password">
                              <KeyRound size={13} />
                            </button>
                            <button
                              className={`btn btn-ghost btn-sm ${u.disabled ? "btn-primary" : "btn-danger"}`}
                              onClick={() => toggleStatus(u)}
                              title={u.disabled ? "Reactivate account" : "Suspend account"}
                            >
                              {u.disabled ? <UserCheck size={14} /> : <UserX size={14} />}
                            </button>
                          </>
                        )}
                      </div>
                    </td>
                  </tr>
                );
              })}
          </tbody>
        </table>
      </div>

      {createOpen && <CreateUserModal onClose={() => setCreateOpen(false)} onSubmit={handleCreateUser} />}
      {editLimitsUser && (
        <ConfigureLimitsModal
          user={editLimitsUser}
          onClose={() => setEditLimitsUser(null)}
          onSubmit={(r, q, s) => handleSaveLimits(editLimitsUser.id, r, q, s)}
        />
      )}
      {resetPassUser && (
        <ResetPasswordModal
          user={resetPassUser}
          onClose={() => setResetPassUser(null)}
          onSubmit={(p) => handleResetPassword(resetPassUser.id, p)}
          notify={notify}
        />
      )}
    </>
  );
}

function UserApiKeysPage({
  user,
  api,
  notify,
  onBack,
  onUserUpdated,
}: {
  user: User;
  api: (p: string, opt?: RequestInit) => Promise<Response>;
  notify: (m: string) => void;
  onBack: () => void;
  onUserUpdated: (u: User) => void;
}) {
  const [keys, setKeys] = useState<ApiKey[]>([]);
  const [loading, setLoading] = useState(true);
  const [createKeyOpen, setCreateKeyOpen] = useState(false);
  const [editingKey, setEditingKey] = useState<ApiKey | null>(null);
  const [editLimitsOpen, setEditLimitsOpen] = useState(false);
  const [newKey, setNewKey] = useState<string | null>(null);

  const loadKeys = useCallback(async () => {
    setLoading(true);
    const res = await api(`/v1/users/${user.id}/apikeys`);
    if (res.ok) {
      const data = await res.json();
      setKeys(data.api_keys || []);
    }
    setLoading(false);
  }, [api, user.id]);

  useEffect(() => { loadKeys(); }, [loadKeys]);

  async function handleCreateKey(name: string, rpm: number) {
    const res = await api(`/v1/users/${user.id}/apikeys`, {
      method: "POST",
      body: JSON.stringify({ name, rate_limit_rpm: rpm }),
    });
    const d = await res.json().catch(() => ({}));
    if (res.ok) {
      setCreateKeyOpen(false);
      setNewKey(d.key);
      loadKeys();
    } else {
      notify(d.detail || "Failed to create key");
    }
  }

  async function handleUpdateKeyLimit(keyId: number, rpm: number) {
    const res = await api(`/v1/users/${user.id}/apikeys/${keyId}`, {
      method: "PATCH",
      body: JSON.stringify({ rate_limit_rpm: rpm }),
    });
    if (res.ok) {
      setEditingKey(null);
      notify("API Key rate limit updated & synchronized");
      loadKeys();
    } else {
      const d = await res.json().catch(() => ({}));
      notify(d.detail || "Failed to update key limit");
    }
  }

  async function handleSaveAccountLimits(rate: number, quota: number, storageGB: number) {
    const res = await api(`/v1/users/${user.id}/config`, {
      method: "PUT",
      body: JSON.stringify({
        rate_limit_rpm: rate,
        doc_quota: quota,
        storage_quota_bytes: storageGB * 1024 * 1024 * 1024,
      }),
    });
    if (res.ok) {
      const updatedCfg = await res.json().catch(() => ({}));
      onUserUpdated({ ...user, config: updatedCfg });
      setEditLimitsOpen(false);
      notify("Account limits & quotas updated");
    } else {
      notify("Failed to update account limits");
    }
  }

  async function handleRevoke(k: ApiKey) {
    if (!window.confirm(`Revoke secret key "${k.name}"? Active workloads using this token will fail immediately.`)) return;
    const res = await api(`/v1/users/${user.id}/apikeys/${k.id}`, { method: "DELETE" });
    if (res.ok || res.status === 204) {
      notify("API Key revoked");
      loadKeys();
    }
  }

  const isUnlimitedRate = !user.config?.rate_limit_rpm || user.config.rate_limit_rpm === 0;
  const isUnlimitedDoc = !user.config?.doc_quota || user.config.doc_quota === 0;
  const isUnlimitedStorage = !user.config?.storage_quota_bytes || user.config.storage_quota_bytes === 0;

  return (
    <>
      <div style={{ marginBottom: "20px" }}>
        <button className="btn btn-ghost btn-sm" onClick={onBack} style={{ paddingLeft: 0 }}>
          <ArrowLeft size={14} />
          <span>Back to All Accounts</span>
        </button>
      </div>

      <div className="page-header">
        <div>
          <div className="eyebrow">
            <Key size={12} />
            <span>Developer Credentials</span>
          </div>
          <h1>{user.email}</h1>
          <p className="page-desc">Secret keys authenticate OCR requests. You can manage per-key rate limits or adjust total account quotas.</p>
        </div>
        <div style={{ display: "flex", gap: "10px" }}>
          {user.role !== "admin" && (
            <button className="btn btn-secondary" onClick={() => setEditLimitsOpen(true)}>
              <Sliders size={14} />
              <span>Edit Account Quotas</span>
            </button>
          )}
          <button className="btn btn-primary" onClick={() => setCreateKeyOpen(true)}>
            <Plus size={15} />
            <span>Create Secret Key</span>
          </button>
        </div>
      </div>

      {/* Account Overview Summary Card */}
      <div className="panel" style={{ padding: "20px 24px", marginBottom: "24px", display: "grid", gridTemplateColumns: "repeat(4, 1fr)", gap: "20px", background: "#ffffff" }}>
        <div>
          <div style={{ fontSize: "11px", textTransform: "uppercase", color: "var(--text-dim)", fontWeight: 650, marginBottom: "4px" }}>Account Rate Limit</div>
          <strong style={{ fontSize: "16px", color: isUnlimitedRate ? "#7c3aed" : "var(--text-main)" }}>
            {isUnlimitedRate ? "∞ Unlimited" : `${user.config?.rate_limit_rpm} RPM`}
          </strong>
        </div>
        <div>
          <div style={{ fontSize: "11px", textTransform: "uppercase", color: "var(--text-dim)", fontWeight: 650, marginBottom: "4px" }}>Document Quota</div>
          <strong style={{ fontSize: "16px", color: "var(--text-main)" }}>
            {(user.config?.doc_used || 0).toLocaleString()} / {isUnlimitedDoc ? "∞" : (user.config?.doc_quota || 0).toLocaleString()}
          </strong>
        </div>
        <div>
          <div style={{ fontSize: "11px", textTransform: "uppercase", color: "var(--text-dim)", fontWeight: 650, marginBottom: "4px" }}>Storage Boundary</div>
          <strong style={{ fontSize: "16px", color: "var(--text-main)" }}>
            {isUnlimitedStorage ? "Unlimited" : formatBytes(user.config?.storage_quota_bytes)}
          </strong>
        </div>
        <div>
          <div style={{ fontSize: "11px", textTransform: "uppercase", color: "var(--text-dim)", fontWeight: 650, marginBottom: "4px" }}>Storage Utilized</div>
          <strong style={{ fontSize: "16px", color: "var(--success)" }}>
            {formatBytes(user.config?.storage_used_bytes || 0)}
          </strong>
        </div>
      </div>

      {newKey && <KeyGeneratedBanner secretKey={newKey} onClose={() => setNewKey(null)} notify={notify} />}

      <div className="panel">
        <div className="panel-header">
          <span style={{ fontSize: "13px", fontWeight: 650 }}>Issued Secret Keys ({keys.length})</span>
          <button className="btn btn-ghost btn-sm" onClick={loadKeys}>
            <RefreshCw size={13} />
            <span>Refresh</span>
          </button>
        </div>
        <table className="data-table">
          <thead>
            <tr>
              <th>Key Label</th>
              <th>Secret Prefix</th>
              <th>Key Rate Limit</th>
              <th>Created At</th>
              <th>Status</th>
              <th style={{ textAlign: "right" }}>Manage Key</th>
            </tr>
          </thead>
          <tbody>
            {loading && (
              <tr>
                <td colSpan={6} style={{ textAlign: "center", padding: "32px", color: "var(--text-dim)" }}>
                  Loading API keys…
                </td>
              </tr>
            )}
            {!loading && keys.length === 0 && (
              <tr>
                <td colSpan={6} style={{ textAlign: "center", padding: "40px 20px" }}>
                  <div style={{ color: "var(--text-dim)", marginBottom: "12px" }}>No secret keys issued for this account yet.</div>
                  <button className="btn btn-secondary btn-sm" onClick={() => setCreateKeyOpen(true)}>
                    <Plus size={13} />
                    <span>Create first key</span>
                  </button>
                </td>
              </tr>
            )}
            {!loading &&
              keys.map((k) => (
                <tr key={k.id}>
                  <td>
                    <strong style={{ color: "var(--text-main)" }}>{k.name || "Default Key"}</strong>
                  </td>
                  <td>
                    <span className="code-badge">{k.prefix || "sk_ocr_••••••••"}</span>
                  </td>
                  <td>
                    <span className="code-badge" style={{ color: k.rate_limit_rpm === 0 ? "#7c3aed" : "#2563eb", background: k.rate_limit_rpm === 0 ? "#f5f3ff" : "#eff6ff", borderColor: k.rate_limit_rpm === 0 ? "#ddd6fe" : "#bfdbfe" }}>
                      {k.rate_limit_rpm === 0 ? "∞ Unlimited" : `${k.rate_limit_rpm} RPM`}
                    </span>
                  </td>
                  <td>
                    <span style={{ fontSize: "13px" }}>{formatDate(k.created_at)}</span>
                  </td>
                  <td>
                    <span className={`badge ${k.revoked_at ? "badge-disabled" : "badge-active"}`}>
                      {k.revoked_at ? "Revoked" : "Active"}
                    </span>
                  </td>
                  <td style={{ textAlign: "right" }}>
                    <div style={{ display: "inline-flex", gap: "6px" }}>
                      {!k.revoked_at && (
                        <>
                          <button
                            className="btn btn-secondary btn-sm"
                            onClick={() => setEditingKey(k)}
                            title="Edit Rate Limit specifically for this key"
                          >
                            <Sliders size={13} />
                            <span>Limit</span>
                          </button>
                          <button
                            className="btn btn-ghost btn-sm btn-danger"
                            onClick={() => handleRevoke(k)}
                            title="Revoke secret key"
                          >
                            <Trash2 size={14} />
                          </button>
                        </>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
          </tbody>
        </table>
      </div>

      {createKeyOpen && (
        <CreateKeyModal
          defaultRpm={user.config?.rate_limit_rpm || 60}
          onClose={() => setCreateKeyOpen(false)}
          onSubmit={handleCreateKey}
        />
      )}

      {editingKey && (
        <EditKeyLimitModal
          apiKey={editingKey}
          onClose={() => setEditingKey(null)}
          onSubmit={(rpm) => handleUpdateKeyLimit(editingKey.id, rpm)}
        />
      )}

      {editLimitsOpen && (
        <ConfigureLimitsModal
          user={user}
          onClose={() => setEditLimitsOpen(false)}
          onSubmit={handleSaveAccountLimits}
        />
      )}
    </>
  );
}

// Reusable Quota Selector Component with explicit Unlimited vs Custom Limit choice
function QuotaSelector({
  label,
  value,
  unit,
  onChange,
}: {
  label: string;
  value: number;
  unit: string;
  onChange: (val: number) => void;
}) {
  const isUnlimited = value === 0;

  return (
    <div className="form-group" style={{ marginBottom: "18px" }}>
      <label style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
        <span style={{ color: "var(--text-main)", fontWeight: 600 }}>{label}</span>
        <div style={{ display: "flex", background: "#f1f5f9", padding: "2px", borderRadius: "6px", border: "1px solid var(--border)" }}>
          <button
            type="button"
            className="btn btn-sm"
            style={{
              height: "26px",
              padding: "0 10px",
              fontSize: "11px",
              background: isUnlimited ? "#ffffff" : "transparent",
              color: isUnlimited ? "var(--accent)" : "var(--text-muted)",
              boxShadow: isUnlimited ? "0 1px 2px rgba(0,0,0,0.08)" : "none",
              border: 0,
              fontWeight: isUnlimited ? 600 : 500,
            }}
            onClick={() => onChange(0)}
          >
            ∞ Unlimited
          </button>
          <button
            type="button"
            className="btn btn-sm"
            style={{
              height: "26px",
              padding: "0 10px",
              fontSize: "11px",
              background: !isUnlimited ? "#ffffff" : "transparent",
              color: !isUnlimited ? "var(--accent)" : "var(--text-muted)",
              boxShadow: !isUnlimited ? "0 1px 2px rgba(0,0,0,0.08)" : "none",
              border: 0,
              fontWeight: !isUnlimited ? 600 : 500,
            }}
            onClick={() => {
              if (isUnlimited) onChange(60);
            }}
          >
            Custom Limit
          </button>
        </div>
      </label>

      {isUnlimited ? (
        <div
          style={{
            height: "40px",
            background: "#f5f3ff",
            border: "1px dashed #c4b5fd",
            borderRadius: "8px",
            display: "flex",
            alignItems: "center",
            padding: "0 14px",
            color: "#7c3aed",
            fontSize: "13px",
            fontWeight: 550,
            gap: "8px",
          }}
        >
          <InfinityIcon size={16} />
          <span>No restrictions applied (Uncapped throughput)</span>
        </div>
      ) : (
        <div style={{ position: "relative", display: "flex", alignItems: "center" }}>
          <input
            className="form-input"
            type="number"
            min="1"
            value={value}
            onChange={(e) => onChange(Math.max(1, Number(e.target.value)))}
            style={{ width: "100%", paddingRight: "50px" }}
            required
          />
          <span style={{ position: "absolute", right: "14px", color: "var(--text-dim)", fontSize: "12px", pointerEvents: "none" }}>
            {unit}
          </span>
        </div>
      )}
    </div>
  );
}

function CreateUserModal({ onClose, onSubmit }: { onClose: () => void; onSubmit: (vals: any) => Promise<void> }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState(() => generateStrongPassword());
  const [showPassword, setShowPassword] = useState(true);
  const [rate, setRate] = useState(60);
  const [quota, setQuota] = useState(1000);
  const [storageGB, setStorageGB] = useState(10);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await onSubmit({
        email,
        password,
        role: "user",
        rate_limit_rpm: rate,
        doc_quota: quota,
        storage_quota_bytes: storageGB * 1024 * 1024 * 1024,
      });
    } catch (err: any) {
      setError(err.message);
      setBusy(false);
    }
  }

  return (
    <div className="modal-overlay" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="modal-box" style={{ maxWidth: 540 }}>
        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "flex-start" }}>
          <h2>Create User Account</h2>
          <button className="btn btn-ghost btn-sm" onClick={onClose}>
            <X size={16} />
          </button>
        </div>
        <p>Provision a new tenant account with credentials and baseline resource boundaries.</p>

        <form onSubmit={submit}>
          <div className="form-group">
            <label>User Email</label>
            <input
              autoFocus
              className="form-input"
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="developer@company.com"
              required
            />
          </div>

          <div className="form-group">
            <label style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
              <span>Initial Password (Prefilled Strong Password)</span>
              <button
                type="button"
                className="btn btn-ghost btn-sm"
                style={{ fontSize: "11px", color: "var(--accent)", fontWeight: 600 }}
                onClick={() => setPassword(generateStrongPassword())}
              >
                Regenerate
              </button>
            </label>
            <div style={{ position: "relative", display: "flex", alignItems: "center" }}>
              <input
                className="form-input"
                type={showPassword ? "text" : "password"}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                style={{ width: "100%", paddingRight: "40px", fontFamily: "var(--font-mono)", fontSize: "13px" }}
                required
              />
              <button
                type="button"
                className="btn btn-ghost btn-sm"
                style={{ position: "absolute", right: "6px" }}
                onClick={() => setShowPassword(!showPassword)}
              >
                {showPassword ? <EyeOff size={14} /> : <Eye size={14} />}
              </button>
            </div>
            <span style={{ fontSize: "11px", color: "var(--text-dim)" }}>Make sure to copy and send this password to the user.</span>
          </div>

          <div style={{ borderTop: "1px solid var(--border)", paddingTop: "18px", marginTop: "18px" }}>
            <QuotaSelector label="Request Rate Limit" value={rate} unit="RPM" onChange={setRate} />
            <QuotaSelector label="Document Quota" value={quota} unit="Docs" onChange={setQuota} />
            <QuotaSelector label="S3 Storage Limit" value={storageGB} unit="GiB" onChange={setStorageGB} />
          </div>

          {error && <div style={{ color: "var(--danger)", fontSize: "12px", marginBottom: "16px" }}>{error}</div>}

          <div style={{ display: "flex", justifyContent: "flex-end", gap: "10px", marginTop: "24px" }}>
            <button type="button" className="btn btn-secondary" onClick={onClose}>
              Cancel
            </button>
            <button disabled={busy} type="submit" className="btn btn-primary">
              {busy ? "Provisioning…" : "Create User"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

function ConfigureLimitsModal({
  user,
  onClose,
  onSubmit,
}: {
  user: User;
  onClose: () => void;
  onSubmit: (r: number, q: number, s: number) => Promise<void>;
}) {
  const [rate, setRate] = useState(user.config?.rate_limit_rpm ?? 60);
  const [quota, setQuota] = useState(user.config?.doc_quota ?? 1000);
  const curGB = user.config?.storage_quota_bytes ? Math.round(user.config.storage_quota_bytes / (1024 * 1024 * 1024)) : 0;
  const [storageGB, setStorageGB] = useState(curGB);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    await onSubmit(Number(rate), Number(quota), Number(storageGB));
    setBusy(false);
  }

  return (
    <div className="modal-overlay" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="modal-box" style={{ maxWidth: 500 }}>
        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "flex-start" }}>
          <h2>Configure Quotas & Limits</h2>
          <button className="btn btn-ghost btn-sm" onClick={onClose}>
            <X size={16} />
          </button>
        </div>
        <p>Configuring resource boundaries for <strong>{user.email}</strong></p>

        <form onSubmit={submit}>
          <QuotaSelector label="Request Rate Limit" value={rate} unit="RPM" onChange={setRate} />
          <QuotaSelector label="Document Quota" value={quota} unit="Docs" onChange={setQuota} />
          <QuotaSelector label="S3 Storage Limit" value={storageGB} unit="GiB" onChange={setStorageGB} />

          <div style={{ display: "flex", justifyContent: "flex-end", gap: "10px", marginTop: "24px" }}>
            <button type="button" className="btn btn-secondary" onClick={onClose}>
              Cancel
            </button>
            <button disabled={busy} type="submit" className="btn btn-primary">
              {busy ? "Saving…" : "Save Changes"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

function ResetPasswordModal({
  user,
  onClose,
  onSubmit,
  notify,
}: {
  user: User;
  onClose: () => void;
  onSubmit: (pass: string) => Promise<void>;
  notify: (m: string) => void;
}) {
  const [password, setPassword] = useState(() => generateStrongPassword());
  const [showPassword, setShowPassword] = useState(true);
  const [busy, setBusy] = useState(false);
  const [copied, setCopied] = useState(false);

  function copyPassword() {
    navigator.clipboard.writeText(password);
    setCopied(true);
    notify("Password copied to clipboard");
    setTimeout(() => setCopied(false), 2000);
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      await onSubmit(password);
    } catch (err: any) {
      notify(err.message);
      setBusy(false);
    }
  }

  return (
    <div className="modal-overlay" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="modal-box" style={{ maxWidth: 480 }}>
        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "flex-start" }}>
          <h2>Reset User Password</h2>
          <button className="btn btn-ghost btn-sm" onClick={onClose}>
            <X size={16} />
          </button>
        </div>
        <p>Set a new secure password for <strong>{user.email}</strong>.</p>

        <form onSubmit={submit}>
          <div className="form-group">
            <label style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
              <span>New Password</span>
              <button
                type="button"
                className="btn btn-ghost btn-sm"
                style={{ fontSize: "11px", color: "var(--accent)", fontWeight: 600 }}
                onClick={() => setPassword(generateStrongPassword())}
              >
                Regenerate
              </button>
            </label>
            <div style={{ position: "relative", display: "flex", alignItems: "center" }}>
              <input
                className="form-input"
                type={showPassword ? "text" : "password"}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                style={{ width: "100%", paddingRight: "40px", fontFamily: "var(--font-mono)", fontSize: "13px" }}
                required
              />
              <button
                type="button"
                className="btn btn-ghost btn-sm"
                style={{ position: "absolute", right: "6px" }}
                onClick={() => setShowPassword(!showPassword)}
              >
                {showPassword ? <EyeOff size={14} /> : <Eye size={14} />}
              </button>
            </div>
          </div>

          <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginTop: "24px" }}>
            <button type="button" className="btn btn-secondary btn-sm" onClick={copyPassword}>
              {copied ? <Check size={13} color="var(--success)" /> : <Copy size={13} />}
              <span>{copied ? "Copied" : "Copy Password"}</span>
            </button>
            <div style={{ display: "flex", gap: "10px" }}>
              <button type="button" className="btn btn-secondary" onClick={onClose}>
                Cancel
              </button>
              <button disabled={busy} type="submit" className="btn btn-primary">
                {busy ? "Saving…" : "Update Password"}
              </button>
            </div>
          </div>
        </form>
      </div>
    </div>
  );
}

function CreateKeyModal({
  defaultRpm,
  onClose,
  onSubmit,
}: {
  defaultRpm: number;
  onClose: () => void;
  onSubmit: (name: string, rpm: number) => Promise<void>;
}) {
  const [name, setName] = useState("");
  const [rpm, setRpm] = useState(defaultRpm);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    await onSubmit(name || "Production API Key", Number(rpm));
    setBusy(false);
  }

  return (
    <div className="modal-overlay" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="modal-box">
        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "flex-start" }}>
          <h2>Create Secret API Key</h2>
          <button className="btn btn-ghost btn-sm" onClick={onClose}>
            <X size={16} />
          </button>
        </div>
        <p>Assign a descriptive label and an individual request rate limit to this token.</p>
        <form onSubmit={submit}>
          <div className="form-group">
            <label>Key Label / Application Name</label>
            <input
              autoFocus
              className="form-input"
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="e.g. Mobile App Backend, OCR Webhook Worker"
            />
          </div>

          <QuotaSelector label="Key Rate Limit" value={rpm} unit="RPM" onChange={setRpm} />

          <div style={{ display: "flex", justifyContent: "flex-end", gap: "10px", marginTop: "24px" }}>
            <button type="button" className="btn btn-secondary" onClick={onClose}>
              Cancel
            </button>
            <button disabled={busy} type="submit" className="btn btn-primary">
              {busy ? "Generating…" : "Create Secret Key"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

function EditKeyLimitModal({
  apiKey,
  onClose,
  onSubmit,
}: {
  apiKey: ApiKey;
  onClose: () => void;
  onSubmit: (rpm: number) => Promise<void>;
}) {
  const [rpm, setRpm] = useState(apiKey.rate_limit_rpm);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    await onSubmit(Number(rpm));
    setBusy(false);
  }

  return (
    <div className="modal-overlay" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="modal-box">
        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "flex-start" }}>
          <h2>Edit API Key Rate Limit</h2>
          <button className="btn btn-ghost btn-sm" onClick={onClose}>
            <X size={16} />
          </button>
        </div>
        <p>Adjust request rate limit specifically for secret key <strong>{apiKey.name}</strong> ({apiKey.prefix}).</p>
        <form onSubmit={submit}>
          <QuotaSelector label="Key Rate Limit" value={rpm} unit="RPM" onChange={setRpm} />

          <div style={{ display: "flex", justifyContent: "flex-end", gap: "10px", marginTop: "24px" }}>
            <button type="button" className="btn btn-secondary" onClick={onClose}>
              Cancel
            </button>
            <button disabled={busy} type="submit" className="btn btn-primary">
              {busy ? "Saving…" : "Update Key Limit"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

function KeyGeneratedBanner({ secretKey, onClose, notify }: { secretKey: string; onClose: () => void; notify: (m: string) => void }) {
  const [copied, setCopied] = useState(false);

  function copy() {
    navigator.clipboard.writeText(secretKey);
    setCopied(true);
    notify("Secret key copied to clipboard");
    setTimeout(() => setCopied(false), 2000);
  }

  return (
    <div
      style={{
        background: "#f0fdf4",
        border: "1px solid #86efac",
        borderRadius: "12px",
        padding: "20px 24px",
        marginBottom: "24px",
      }}
    >
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: "8px" }}>
        <div style={{ display: "flex", alignItems: "center", gap: 8, color: "var(--success)", fontWeight: 650 }}>
          <Check size={16} />
          <span>Save your secret key</span>
        </div>
        <button className="btn btn-ghost btn-sm" onClick={onClose}>
          <X size={14} />
        </button>
      </div>
      <p style={{ margin: "0 0 16px 0", fontSize: "13px", color: "var(--text-muted)" }}>
        Please save this secret key immediately. For security reasons, you won't be able to view it again.
      </p>
      <div className="key-reveal-box">
        <span>{secretKey}</span>
        <button className="btn btn-secondary btn-sm" onClick={copy} style={{ flexShrink: 0 }}>
          {copied ? <Check size={13} color="var(--success)" /> : <Copy size={13} />}
          <span>{copied ? "Copied" : "Copy"}</span>
        </button>
      </div>
    </div>
  );
}

function Login({ onSubmit }: { onSubmit: (e: string, p: string) => Promise<void> }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await onSubmit(email, password);
    } catch (err: any) {
      setError(err.message);
      setBusy(false);
    }
  }

  return (
    <div style={{ minHeight: "100vh", display: "flex", alignItems: "center", justifyContent: "center", background: "#f8fafc", padding: 20 }}>
      <div className="modal-box" style={{ maxWidth: 400, width: "100%" }}>
        <div style={{ display: "flex", alignItems: "center", gap: 12, marginBottom: 24 }}>
          <span className="brand-symbol">
            <Shield size={20} />
          </span>
          <div>
            <div style={{ fontWeight: 750, fontSize: 18, color: "var(--text-main)" }}>MacOCR</div>
            <div style={{ fontSize: 12, color: "var(--text-dim)" }}>Cluster Control Plane</div>
          </div>
        </div>
        <form onSubmit={submit}>
          <div className="form-group">
            <label>Administrator Email</label>
            <input
              autoFocus
              className="form-input"
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="admin@dungxbuif.com"
              required
            />
          </div>
          <div className="form-group">
            <label>Password</label>
            <input
              className="form-input"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder="••••••••••••"
              required
            />
          </div>
          {error && <div style={{ color: "var(--danger)", fontSize: 12, marginBottom: 16 }}>{error}</div>}
          <button disabled={busy} type="submit" className="btn btn-primary" style={{ width: "100%", marginTop: 8 }}>
            {busy ? "Authenticating…" : "Sign In to Dashboard"}
          </button>
        </form>
      </div>
    </div>
  );
}

createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>
);
