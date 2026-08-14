let currentCSRFToken = "";

document.addEventListener("DOMContentLoaded", () => {
  initAuth();
  initNavigation();
});

function getCookie(name) {
  const match = document.cookie.match(new RegExp('(^| )' + name + '=([^;]+)'));
  return match ? match[2] : null;
}

async function apiFetch(url, options = {}) {
  options.headers = options.headers || {};
  if (!options.headers["Content-Type"] && !(options.body instanceof FormData)) {
    options.headers["Content-Type"] = "application/json";
  }
  if (currentCSRFToken) {
    options.headers["X-CSRF-Token"] = currentCSRFToken;
  }
  const res = await fetch(url, options);
  if (res.status === 401 && !url.includes("/v1/auth/login")) {
    showLoginModal();
    throw new Error("Unauthorized");
  }
  return res;
}

async function initAuth() {
  currentCSRFToken = getCookie("macocr_csrf") || "";
  try {
    const res = await apiFetch("/v1/auth/me");
    if (res.ok) {
      const data = await res.json();
      document.getElementById("user-email").textContent = data.email;
      hideLoginModal();
      handleRoute();
    } else {
      showLoginModal();
    }
  } catch {
    showLoginModal();
  }

  document.getElementById("form-login").addEventListener("submit", async (e) => {
    e.preventDefault();
    const email = document.getElementById("login-email").value;
    const password = document.getElementById("login-pass").value;
    const errEl = document.getElementById("login-error");

    try {
      const res = await fetch("/v1/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, password }),
      });
      if (!res.ok) {
        const errData = await res.json().catch(() => ({}));
        errEl.textContent = errData.detail || "Invalid email or password";
        errEl.style.display = "block";
        return;
      }
      const data = await res.json();
      currentCSRFToken = data.csrfToken;
      document.getElementById("user-email").textContent = data.user.email;
      hideLoginModal();
      showToast("Signed in successfully");
      handleRoute();
    } catch (err) {
      errEl.textContent = "Login request failed";
      errEl.style.display = "block";
    }
  });

  document.getElementById("btn-logout").addEventListener("click", async () => {
    await apiFetch("/v1/auth/logout", { method: "POST" });
    showLoginModal();
  });
}

function showLoginModal() {
  document.getElementById("login-modal").setAttribute("open", "true");
}
function hideLoginModal() {
  document.getElementById("login-modal").removeAttribute("open");
  document.getElementById("login-error").style.display = "none";
}

function initNavigation() {
  window.addEventListener("hashchange", handleRoute);
  document.getElementById("btn-open-create-user").addEventListener("click", () => {
    document.getElementById("modal-create-user").setAttribute("open", "true");
  });

  document.getElementById("form-create-user").addEventListener("submit", async (e) => {
    e.preventDefault();
    const email = document.getElementById("cu-email").value;
    const rate = parseInt(document.getElementById("cu-rate").value, 10);
    const quota = parseInt(document.getElementById("cu-quota").value, 10);

    try {
      const res = await apiFetch("/v1/users", {
        method: "POST",
        body: JSON.stringify({ email, rate_limit_rpm: rate, doc_quota: quota }),
      });
      if (!res.ok) {
        const err = await res.json();
        alert("Error: " + (err.detail || "Failed to create user"));
        return;
      }
      document.getElementById("modal-create-user").removeAttribute("open");
      showToast("User created: " + email);
      loadUsers();
    } catch (err) {
      alert("Failed to create user");
    }
  });
}

function handleRoute() {
  const hash = location.hash || "#/dashboard";
  document.querySelectorAll(".nav-link").forEach((a) => {
    a.classList.toggle("active", a.getAttribute("href") === hash);
  });
  document.querySelectorAll(".view-section").forEach((sec) => (sec.style.display = "none"));

  if (hash === "#/users") {
    document.getElementById("view-users").style.display = "block";
    loadUsers();
  } else if (hash === "#/documents") {
    document.getElementById("view-documents").style.display = "block";
    loadDocuments();
  } else {
    document.getElementById("view-dashboard").style.display = "block";
    refreshDashboard();
  }
}

async function refreshDashboard() {
  try {
    const res = await apiFetch("/v1/admin/dashboard");
    if (!res.ok) return;
    const data = await res.json();
    document.getElementById("stat-users").textContent = data.totalUsers || 0;
    document.getElementById("stat-queued").textContent = data.queueCounts?.queued || 0;
    document.getElementById("stat-processing").textContent = data.queueCounts?.processing || 0;
    document.getElementById("stat-completed").textContent = data.queueCounts?.completed || 0;
  } catch {}
}

async function loadUsers() {
  const tbody = document.getElementById("users-table-body");
  try {
    const res = await apiFetch("/v1/users?limit=100");
    if (!res.ok) return;
    const data = await res.json();
    const users = data.users || [];
    if (users.length === 0) {
      tbody.innerHTML = `<tr><td colspan="8" style="text-align: center;">No users registered yet.</td></tr>`;
      return;
    }

    tbody.innerHTML = users.map(u => {
      const rpm = u.config ? u.config.rate_limit_rpm : 60;
      const quota = u.config ? (u.config.doc_quota === 0 ? "Unlimited" : u.config.doc_quota) : "Unlimited";
      const used = u.config ? u.config.doc_used : 0;
      const statusBadge = u.disabled ? `<span class="badge badge-failed">Disabled</span>` : `<span class="badge badge-completed">Active</span>`;

      return `
        <tr>
          <td>${u.id}</td>
          <td><strong>${escapeHtml(u.email)}</strong></td>
          <td>${u.role}</td>
          <td>${statusBadge}</td>
          <td>${rpm}</td>
          <td>${quota}</td>
          <td>${used}</td>
          <td>
            <button class="outline" style="padding: 0.25rem 0.5rem; font-size: 0.75rem; margin: 0;" onclick="createKeyForUser(${u.id})">+ Key</button>
            <button class="outline secondary" style="padding: 0.25rem 0.5rem; font-size: 0.75rem; margin: 0;" onclick="resetQuota(${u.id})">Reset Quota</button>
            ${u.disabled ? "" : `<button class="outline secondary" style="padding: 0.25rem 0.5rem; font-size: 0.75rem; margin: 0;" onclick="deactivateUser(${u.id})">Deactivate</button>`}
          </td>
        </tr>
      `;
    }).join("");
  } catch {
    tbody.innerHTML = `<tr><td colspan="8" style="text-align: center; color: #ef4444;">Failed to load users.</td></tr>`;
  }
}

async function createKeyForUser(userID) {
  const name = prompt("Enter a key identifier/name:", "api-key");
  if (!name) return;

  try {
    const res = await apiFetch(`/v1/users/${userID}/apikeys`, {
      method: "POST",
      body: JSON.stringify({ name: name, rate_limit_rpm: 60 }),
    });
    if (!res.ok) {
      alert("Failed to create key");
      return;
    }
    const data = await res.json();
    document.getElementById("key-display-text").textContent = data.key;
    document.getElementById("modal-key-display").setAttribute("open", "true");
  } catch (err) {
    alert("Error creating API key");
  }
}

async function resetQuota(userID) {
  if (!confirm(`Reset document counter for User ID ${userID} to 0?`)) return;
  try {
    const res = await apiFetch(`/v1/users/${userID}/config/reset-quota`, { method: "POST" });
    if (res.ok) {
      showToast("Quota counter reset to 0");
      loadUsers();
    }
  } catch {
    alert("Reset quota failed");
  }
}

async function deactivateUser(userID) {
  if (!confirm(`Deactivate User ID ${userID}? Existing queued documents will continue, but all new authenticated requests will be rejected.`)) return;
  try {
    const res = await apiFetch(`/v1/users/${userID}/deactivate`, { method: "POST" });
    if (!res.ok) {
      alert("Account deactivation failed");
      return;
    }
    showToast("Account deactivated");
    loadUsers();
  } catch {
    alert("Account deactivation failed");
  }
}

async function loadDocuments() {
  const tbody = document.getElementById("documents-table-body");
  try {
    const res = await apiFetch("/v1/admin/documents?limit=50");
    if (!res.ok) return;
    const data = await res.json();
    const docs = data.documents || [];
    if (docs.length === 0) {
      tbody.innerHTML = `<tr><td colspan="7" style="text-align: center;">Queue is empty.</td></tr>`;
      return;
    }

    tbody.innerHTML = docs.map(d => {
      let badgeClass = "badge-queued";
      if (d.status === "processing") badgeClass = "badge-processing";
      else if (d.status === "completed") badgeClass = "badge-completed";
      else if (d.status === "failed") badgeClass = "badge-failed";
      else if (d.status === "cancelled") badgeClass = "badge-cancelled";

      const preview = d.result_text ? (escapeHtml(d.result_text).substring(0, 40) + "...") : "—";
      return `
        <tr>
          <td><code>${d.id}</code></td>
          <td>${d.user_id}</td>
          <td><span class="badge ${badgeClass}">${d.status}</span></td>
          <td>${d.input_content_type || "—"}</td>
          <td>${d.input_size_bytes ? Math.round(d.input_size_bytes / 1024) + " KB" : "—"}</td>
          <td>${new Date(d.created_at).toLocaleTimeString()}</td>
          <td style="font-size: 0.85rem; color: #94a3b8;">${preview}</td>
        </tr>
      `;
    }).join("");
  } catch {
    tbody.innerHTML = `<tr><td colspan="7" style="text-align: center; color: #ef4444;">Failed to load queue.</td></tr>`;
  }
}

function showToast(msg) {
  const container = document.getElementById("toast-container");
  const el = document.createElement("div");
  el.className = "toast";
  el.textContent = msg;
  container.appendChild(el);
  setTimeout(() => el.remove(), 4000);
}

function escapeHtml(str) {
  if (!str) return "";
  return str.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}
